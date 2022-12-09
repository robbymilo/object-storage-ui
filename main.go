package main

import (
	"bufio"
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/api/iterator"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type GCSObjects []GCSObject

type GCSObject struct {
	Name    string
	Prefix  string
	Value   string
	Updated time.Time
	Size    int64
}

type Objects []Object

type Object struct {
	Name    string
	Value   string
	Updated string
	Size    float64
}

var bucket = "staging-static-grafana-com"
var format = "2006-01-02 15:04"
var pathPrefix = "/upload"

func main() {
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		log.Fatal("GOOGLE_APPLICATION_CREDENTIALS env var missing. Set to the location of the service account json key.")
	}

	fs := http.FileServer(http.Dir("./assets"))
	http.Handle(pathPrefix+"/assets/", http.StripPrefix(pathPrefix+"/assets/", fs))
	// http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/search", handleSearch)
	http.HandleFunc("/", handleRequest)

	log.Print("listening on :3000...")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal(err)
	}
}

// determines if we should send a file or render a template
func handleRequest(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL)

	path := r.URL.Path

	if strings.HasSuffix(path, "/") == true {
		// needs trailing slash on requests as dirs aren't real in GCS

		prefix := ""
		if path[1:] != "" {
			// non-root path
			prefix = path[1:]
		}

		// get list of raw GCS objects
		o := getFiles(prefix, "/")

		// get list of formatted objects for final output
		m := buildGCSMap(o, path)

		if r.Header["Accept"][0] == "application/json" || r.URL.Query().Get("json") == "true" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m)
		} else {
			// send data to html template
			render(w, r, m)
		}

	} else {
		serveFile(w, r)
	}

}

// get multiple files to upload
func handleUpload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(100 << 20)
	files := r.MultipartForm.File["filename"]
	path := r.Form.Get("path")

	for _, fileHeader := range files {
		// check if file exists

		// Open the file
		file, err := fileHeader.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		defer file.Close()

		buff := make([]byte, 512)
		_, err = file.Read(buff)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = file.Seek(0, io.SeekStart)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if path[len(path)-1:] != "/" {
			path = path + "/"
		}

		err = os.MkdirAll("./tmp"+path, os.ModePerm)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		name := fmt.Sprintf("./tmp%s%s", path, fileHeader.Filename)
		f, err := os.Create(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		defer f.Close()

		_, err = io.Copy(f, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		final, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("failed creating file: %s", err)
		}

		err = uploadFile(bufio.NewWriter(final), path+fileHeader.Filename)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			log.Fatalf("failed uploading to GCS: %v", err)
			return
		}

	}

	http.Redirect(w, r, path, http.StatusSeeOther)
	log.Print("upload successful")

}

// upload a single file
func uploadFile(w io.Writer, object string) error {
	log.Print("uploading " + object + " to GCS")

	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	f, err := os.Open(fmt.Sprintf("./tmp%s", object))
	if err != nil {
		return fmt.Errorf("os.Open: %v", err)
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// remove slash from beginning of string as it's not a real dir
	o := client.Bucket(bucket).Object(strings.TrimPrefix(object, "/"))

	// Upload an object with storage.Writer.
	wc := o.NewWriter(ctx)
	if _, err = io.Copy(wc, f); err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %v", err)
	}
	fmt.Fprintf(w, "Blob %v uploaded.\n", object)
	return nil
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL.Path)

	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	o := client.Bucket(bucket).Object(r.URL.Path[1:])
	objAttrs, err := o.Attrs(ctx)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	ot := o.ReadCompressed(true)
	rc, err := ot.NewReader(ctx)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", objAttrs.ContentType)
	w.Header().Set("Content-Encoding", objAttrs.ContentEncoding)
	w.Header().Set("Content-Length", strconv.Itoa(int(objAttrs.Size)))
	w.Header().Set("Cache-Control", "max-age: 31536000")
	w.WriteHeader(200)
	if _, err := io.Copy(w, rc); err != nil {
		return
	}
}

// get raw data of a "dir" from GCS
func getFiles(prefix string, delim string) GCSObjects {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Errorf("storage.NewClient: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: delim,
	}

	it := client.Bucket(bucket).Objects(ctx, query)

	objs := []GCSObject{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Errorf("Bucket(%q).Objects(): %v", bucket, err)
		}
		objs = append(
			objs,
			GCSObject{
				Name:    attrs.Name,
				Prefix:  attrs.Prefix,
				Value:   attrs.Name,
				Updated: attrs.Updated,
				Size:    attrs.Size,
			})
	}
	return objs
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	log.Print(r.URL)
	j := r.URL.Query().Get("json")

	query := r.URL.Query().Get("q")
	if query != "" {
		o := getFiles("", "")

		results := o[:0]

		// loop through all the bucket objects and check if the name contains the query
		// if it does, build a new slice and add the item
		for _, item := range o {
			if strings.Contains(item.Name, query) {
				results = append(results, item)
			}
		}

		// get list of formatted objects for final output
		m := buildGCSMap(results, r.URL.Path)

		if r.Header["Accept"][0] == "application/json" || j == "true" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m)
		} else {
			// send data to html template
			render(w, r, m)
		}
	}
}

// transform raw GCS data into a format a template can work with
func buildGCSMap(o GCSObjects, path string) map[string]interface{} {

	Files := []Object{}
	Dirs := []Object{}

	// build template compatiable map
	for _, item := range o {

		updated := item.Updated.Format(format)

		if item.Name != "" && item.Name != path[1:] {
			Files = append(
				Files,
				Object{
					Name:    strings.Replace(item.Name, path[1:], "", -1),
					Value:   item.Name,
					Updated: updated,
					Size:    size(item.Size),
				})
		}
		if item.Prefix != "" {
			Dirs = append(
				Dirs,
				Object{
					Name:    strings.Replace(item.Prefix, path[1:], "", -1),
					Value:   item.Prefix,
					Updated: "",
					Size:    size(item.Size),
				})
		}

	}

	// add current path to map
	Paths := []Object{}
	for _, v := range strings.Split(path, "/") {
		if v != "" {
			Paths = append(
				Paths,
				Object{
					Name:  v,
					Value: v,
				})
		}
	}

	varmap := map[string]interface{}{
		"files":      Files,
		"dirs":       Dirs,
		"paths":      Paths,
		"current":    path,
		"bucket":     bucket,
		"pathPrefix": pathPrefix,
	}

	return varmap
}

// convert raw size to KB
func size(s int64) float64 {
	return math.Round(float64(s) * .001)
}

// render a template based on the given data
func render(w http.ResponseWriter, r *http.Request, v map[string]interface{}) {
	lp := filepath.Join("templates", "layout.html")
	fp := filepath.Join("templates", "template.html")
	tmpl, _ := template.ParseFiles(lp, fp)

	tmpl.ExecuteTemplate(w, "layout", v)
}
