package main

import (
	"bufio"
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"flag"
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

var port = flag.String("port", "3000", "port to listen on")
var bucketName = flag.String("bucket-name", "default value", "name of bucket")
var pathPrefix = flag.String("path-prefix", "", "a path to prefix the application. use if running the application on a subdirectory.")
var domainPrefix = flag.String("domain-prefix", "", "a domain to use for serving files. use if files will be served from different application.")
var allowUpload = flag.Bool("allow-upload", false, "allow users to upload files")
var allowDelete = flag.Bool("allow-delete", false, "allow users to delete files")
var allowSearch = flag.Bool("allow-search", true, "allow users to search files and directories. searches the entire bucket.")
var format = "2006-01-02 15:04"

func main() {
	flag.Parse()

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		log.Fatal("GOOGLE_APPLICATION_CREDENTIALS env var missing. Set to the location of the service account json key.")
	}

	fs := http.FileServer(http.Dir("./assets"))
	http.Handle(*pathPrefix+"/assets/", http.StripPrefix(*pathPrefix+"/assets/", fs))
	if *allowUpload {
		http.HandleFunc(*pathPrefix+"/upload", handleUpload)
	}
	http.HandleFunc(*pathPrefix+"/search", handleSearch)
	http.HandleFunc(*pathPrefix+"/", handleRequest)

	log.Printf("listening on :%s...", *port)
	err := http.ListenAndServe(":"+*port, nil)
	if err != nil {
		log.Fatal(err)
	}
}

// determines if we should send a file or render a template
func handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if *pathPrefix != "" {
		path = strings.Replace(path, *pathPrefix, "", -1)
	}

	log.Print(path)

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
		serveFile(w, r, path)
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

	http.Redirect(w, r, *pathPrefix+path, http.StatusSeeOther)
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
	o := client.Bucket(*bucketName).Object(strings.TrimPrefix(object, "/"))

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

func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	o := client.Bucket(*bucketName).Object(path[1:])
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

	it := client.Bucket(*bucketName).Objects(ctx, query)

	objs := []GCSObject{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Errorf("Bucket(%q).Objects(): %v", *bucketName, err)
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
		"files":        Files,
		"dirs":         Dirs,
		"paths":        Paths,
		"current":      path,
		"bucket":       *bucketName,
		"pathPrefix":   *pathPrefix,
		"domainPrefix": *domainPrefix,
		"allowUpload":  *allowUpload,
		"allowDelete":  *allowDelete,
		"allowSearch":  *allowSearch,
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
