package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
  "strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type File struct {
	Name   string
	Prefix string
}

func main() {
	fs := http.FileServer(http.Dir("./assets"))
	http.Handle("/assets/", http.StripPrefix("/assets/", fs))
	http.HandleFunc("/", handleRequest)

	log.Print("Listening on :3000...")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal(err)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
  if (strings.HasSuffix(r.URL.Path, "/") == true) {
    listDir(w, r)
  } else {
    serveFile(w, r)
  }

}

func serveFile(w http.ResponseWriter, r *http.Request) {
  fmt.Println(r.URL.Path)
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

  if (r.URL.Path[1:] != "") {
    // non-root path
    prefix = r.URL.Path[1:]
  }

  fmt.Println(prefix, delim)

	it := bucket.Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: delim,
	})

	Files := []File{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			panic(err)
		}
		Files = append(
			Files,
			File{
				Name:   attrs.Name,
				Prefix: attrs.Prefix,
			})
		// fmt.Println(attrs.Prefix, attrs.Name)
	}

  lp := filepath.Join("templates", "layout.html")
	fp := filepath.Join("templates", "example.html")


	tmpl, _ := template.ParseFiles(lp, fp)
	varmap := map[string]interface{}{
		"files": Files,
	}
	tmpl.ExecuteTemplate(w, "layout", varmap)
}
