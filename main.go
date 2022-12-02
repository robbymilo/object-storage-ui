package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"io/ioutil"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type Object struct {
	Name  string
	Value string
}

func main() {
	fs := http.FileServer(http.Dir("./assets"))
	http.Handle("/assets/", http.StripPrefix("/assets/", fs))
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/", handleRequest)

	log.Print("Listening on :3000...")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal(err)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/") == true {
		listDir(w, r)
	} else {
		serveFile(w, r)
	}

}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20)
	file, handler, err := r.FormFile("filename")
	if err != nil {
		fmt.Println("Error Retrieving the File")
		fmt.Println(err)
		return
	}
	defer file.Close()
	fmt.Printf("Uploaded File: %+v\n", handler.Filename)
	fmt.Printf("File Size: %+v\n", handler.Size)
	fmt.Printf("MIME Header: %+v\n", handler.Header)

	tempFile, err := ioutil.TempFile("./temp-images", "upload-*.png")
	if err != nil {
		fmt.Println(err)
	}
	defer tempFile.Close()

	fileBytes, err := ioutil.ReadAll(file)
	if err != nil {
		fmt.Println(err)
	}

	tempFile.Write(fileBytes)

	fmt.Fprintf(w, "Successfully Uploaded File\n")

}

func serveFile(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r.URL.Path)

	start := time.Now()
	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Sets the name for the new bucket.
	bucketName := "staging-static-grafana-com"

	// Creates a Bucket instance.
	bucket := client.Bucket(bucketName)
	oh := bucket.Object(r.URL.Path[1:])
	objAttrs, err := oh.Attrs(ctx)
	if err != nil {
		if os.Getenv("LOGGING") == "true" {
			elapsed := time.Since(start)
			log.Println("| 404 |", elapsed.String(), r.Host, r.Method, r.URL.Path)
		}
		http.Error(w, "Not found", 404)
		return
	}
	o := oh.ReadCompressed(true)
	rc, err := o.NewReader(ctx)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", objAttrs.ContentType)
	w.Header().Set("Content-Encoding", objAttrs.ContentEncoding)
	w.Header().Set("Content-Length", strconv.Itoa(int(objAttrs.Size)))
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

	// Sets the name for the new bucket.
	bucketName := "staging-static-grafana-com"

	// Creates a Bucket instance.
	bucket := client.Bucket(bucketName)

	prefix := ""
	delim := "/"

	if r.URL.Path[1:] != "" {
		// non-root path
		prefix = r.URL.Path[1:]
	}

	it := bucket.Objects(ctx, &storage.Query{
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

		if attrs.Name != "" && attrs.Name != r.URL.Path[1:] {
			Files = append(
				Files,
				Object{
					Name:  strings.Replace(attrs.Name, r.URL.Path[1:], "", -1),
					Value: attrs.Name,
				})
		}
		if attrs.Prefix != "" {
			Dirs = append(
				Dirs,
				Object{
					Name:  strings.Replace(attrs.Prefix, r.URL.Path[1:], "", -1),
					Value: attrs.Prefix,
				})
		}
	}

	lp := filepath.Join("templates", "layout.html")
	fp := filepath.Join("templates", "example.html")

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

	tmpl, _ := template.ParseFiles(lp, fp)
	varmap := map[string]interface{}{
		"files": Files,
		"dirs":  Dirs,
		"paths": Paths,
		"current": r.URL.Path,
	}
	tmpl.ExecuteTemplate(w, "layout", varmap)
}
