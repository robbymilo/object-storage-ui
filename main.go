package main

import (
	"bufio"
	"context"
	"fmt"
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

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type Object struct {
	Name    string
	Value   string
	Updated string
	Size    float64
}

var bucket = "staging-static-grafana-com"
var format = "2006-01-02 15:04"

func main() {
	fs := http.FileServer(http.Dir("./assets"))
	http.Handle("/assets/", http.StripPrefix("/assets/", fs))
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/search", handleSearch)
	http.HandleFunc("/", handleRequest)

	log.Print("Listening on :3000...")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal(err)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r.URL)
	if strings.HasSuffix(r.URL.Path, "/") == true {
		listDir(w, r)
	} else {
		serveFile(w, r)
	}

}

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
			log.Fatalf("Failed uploading to GCS: %v", err)
			return
		}

	}

	http.Redirect(w, r, path, http.StatusSeeOther)
	fmt.Fprintf(w, "Upload successful")

}

func uploadFile(w io.Writer, object string) error {
	fmt.Println("uploading " + object + " to GCS")

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
	fmt.Println(r.URL.Path)

	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	o := client.Bucket(bucket).Object(r.URL.Path[1:])
	objAttrs, err := o.Attrs(ctx)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	ot := o.ReadCompressed(true)
	rc, err := ot.NewReader(ctx)
	if err != nil {
		http.Error(w, "Not found", 404)
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

func listDir(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	prefix := ""
	delim := "/"

	if r.URL.Path[1:] != "" {
		// non-root path
		prefix = r.URL.Path[1:]
	}

	it := client.Bucket(bucket).Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: delim,
	})

	Files := []Object{}
	Dirs := []Object{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			panic(err)
		}

		updated := attrs.Updated.Format(format)

		if attrs.Name != "" && attrs.Name != r.URL.Path[1:] {
			Files = append(
				Files,
				Object{
					Name:    strings.Replace(attrs.Name, r.URL.Path[1:], "", -1),
					Value:   attrs.Name,
					Updated: updated,
					Size:    size(attrs.Size),
				})
		}
		if attrs.Prefix != "" {
			Dirs = append(
				Dirs,
				Object{
					Name:    strings.Replace(attrs.Prefix, r.URL.Path[1:], "", -1),
					Value:   attrs.Prefix,
					Updated: updated,
					Size:    size(attrs.Size),
				})
		}

	}

	Paths := []Object{}
	for _, v := range strings.Split(r.URL.Path, "/") {
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
		"files":   Files,
		"dirs":    Dirs,
		"paths":   Paths,
		"current": r.URL.Path,
		"bucket":  bucket,
	}

	render(w, r, varmap)

}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	fmt.Println(r.URL)

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Errorf("storage.NewClient: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	it := client.Bucket(bucket).Objects(ctx, nil)
	Files := []Object{}
	Dirs := []Object{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			panic(err)
		}

		updated := attrs.Updated.Format(format)

		if strings.Contains(attrs.Name, query) == true {
			if attrs.Name != "" && attrs.Name != r.URL.Path[1:] {
				Files = append(
					Files,
					Object{
						Name:    strings.Replace(attrs.Name, r.URL.Path[1:], "", -1),
						Value:   attrs.Name,
						Updated: updated,
						Size:    size(attrs.Size),
					})
			}
			if attrs.Prefix != "" {
				Dirs = append(
					Dirs,
					Object{
						Name:    strings.Replace(attrs.Prefix, r.URL.Path[1:], "", -1),
						Value:   attrs.Prefix,
						Updated: updated,
						Size:    size(attrs.Size),
					})
			}

		}

	}

	Paths := []Object{}
	for _, v := range strings.Split(r.URL.Path, "/") {
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
		"files":   Files,
		"dirs":    Dirs,
		"paths":   Paths,
		"current": r.URL.Path,
		"query":   query,
		"bucket":  bucket,
	}

	render(w, r, varmap)

}

func render(w http.ResponseWriter, r *http.Request, v map[string]interface{}) {
	lp := filepath.Join("templates", "layout.html")
	fp := filepath.Join("templates", "template.html")
	tmpl, _ := template.ParseFiles(lp, fp)

	tmpl.ExecuteTemplate(w, "layout", v)
}

func size(s int64) float64 {
	return math.Round(float64(s) * .001)
}
