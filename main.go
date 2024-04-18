package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/iterator"
)

type GCSObject struct {
	Name    string
	Prefix  string
	Value   string
	Updated time.Time
	Size    int64
}

type Object struct {
	Name    string
	Value   string
	Updated string
	Size    float64
}

type response struct {
	Files        []Object
	Dirs         []Object
	Paths        []Object
	Current      string
	Id           string
	BucketName   string
	PathPrefix   string
	DomainPrefix string
	AllowUpload  bool
	AllowDelete  bool
	AllowSearch  bool
}

var format = "2006-01-02 15:04"

//go:embed templates/*.html
var templatesDir embed.FS

//go:embed assets
var assetsDir embed.FS

func main() {

	app := &cli.App{
		Name:  "object-storage-ui",
		Usage: "A browser interface for Google Cloud Storage (GCS).",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "bucket-name",
				Usage: "The name of the bucket to serve.",
			},
			&cli.StringFlag{
				Name:  "path-prefix",
				Usage: "A path to prefix the application. Use if running the application on a subdirectory.",
				Value: "",
			},
			&cli.StringFlag{
				Name:  "domain-prefix",
				Usage: "A domain to use for serving files. Use if files will be served from different application.",
				Value: "",
			},
			&cli.BoolFlag{
				Name:  "allow-upload",
				Usage: "Allow files to be uploaded.",
				Value: false,
			},
			&cli.BoolFlag{
				Name:  "allow-delete",
				Usage: "Allow files to be deleted.",
				Value: false,
			},
			&cli.BoolFlag{
				Name:  "allow-search",
				Usage: "Allow files to be search.",
				Value: true,
			},
			&cli.StringFlag{
				Usage:    "Location of the service account json key.",
				EnvVars:  []string{"GOOGLE_APPLICATION_CREDENTIALS"},
				Required: true,
			},
		},
		Action: func(cCtx *cli.Context) error {

			if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
				log.Fatal("GOOGLE_APPLICATION_CREDENTIALS env var missing. Set to the location of the service account json key.")
			}

			r := chi.NewRouter()
			r.Use(middleware.Logger)

			var assetsFs = fs.FS(assetsDir)
			assetsContent, err := fs.Sub(assetsFs, "assets")
			if err != nil {
				log.Fatal("error loading assets dir:", err)
			}
			r.Handle(cCtx.String("path-prefix")+"/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsContent))))

			r.Get(cCtx.String("path-prefix")+"/*", func(w http.ResponseWriter, r *http.Request) {
				handleRequest(w, r, *cCtx)
			})

			if cCtx.Bool("allow-upload") {
				r.Post(cCtx.String("path-prefix")+"/upload", func(w http.ResponseWriter, r *http.Request) {
					handleUpload(w, r, *cCtx)
				})
			}

			r.Get(cCtx.String("path-prefix")+"/search", func(w http.ResponseWriter, r *http.Request) {
				handleSearch(w, r, *cCtx)
			})

			r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("ok\n"))
				if err != nil {
					fmt.Println("health check error:", err)
				}
			})

			fmt.Println("listening on :3000")

			err = http.ListenAndServe(":3000", r)
			if err != nil {
				log.Fatal("error loading application on port 3000:", err)
			}

			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal("error loading application:", err)
	}
}

// handleRequest determines if we should send a file or render a template.
func handleRequest(w http.ResponseWriter, r *http.Request, c cli.Context) {
	path := r.URL.Path
	prefix := c.String("path-prefix")

	if prefix != "" {
		path = strings.Replace(path, prefix, "", -1)
	}

	if strings.HasSuffix(path, "/") {

		// needs trailing slash on requests as dirs aren't real in GCS
		prefix := ""
		if path[1:] != "" {
			// non-root path
			prefix = path[1:]
		}

		// get list of raw GCS objects
		// objects := getFiles(prefix, "/", c)
		objects := getFiles(prefix, "/", c)

		// get list of formatted objects for final output
		m := buildResponse(objects, path, r.URL.Query().Get("id"), c)

		// send data to html template
		renderEmbeddedTemplate(w, m)

	} else {
		serveFile(w, path, c)
	}

}

// handleUpload takes multiple files from a post request.
func handleUpload(w http.ResponseWriter, r *http.Request, c cli.Context) {
	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		fmt.Println("error parsing form data:", err)
	}

	files := r.MultipartForm.File["filename"]
	path := r.Form.Get("path")

	id := ""
	if r.URL.Query().Get("id") != "" {
		id = "?id=" + r.URL.Query().Get("id")
	}

	for _, fileHeader := range files {
		// Open the file
		file, err := fileHeader.Open()
		if err != nil {
			fmt.Println("error opening file:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		defer file.Close()

		buff := make([]byte, 512)
		_, err = file.Read(buff)
		if err != nil {
			fmt.Println("error reading file:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = file.Seek(0, io.SeekStart)
		if err != nil {
			fmt.Println("error seeking file:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if path[len(path)-1:] != "/" {
			path = path + "/"
		}

		err = os.MkdirAll("./tmp"+path, os.ModePerm)
		if err != nil {
			fmt.Println("error creating temp dir:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		renamed := strings.Replace(fileHeader.Filename, " ", "-", -1)
		temp_file := fmt.Sprintf("./tmp%s%s", path, renamed)
		f, err := os.Create(temp_file)
		if err != nil {
			fmt.Println("error creating temp file:", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		defer f.Close()

		_, err = io.Copy(f, file)
		if err != nil {
			fmt.Println("error copying file:", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = uploadFile(path+renamed, c)
		if err != nil {
			log.Println("failed uploading to GCS:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = os.Remove(temp_file)
		if err != nil {
			fmt.Println("error deleting temporary file: ", err)
		}

		fmt.Printf("upload %s successful\n", path+renamed)

	}

	http.Redirect(w, r, c.String("path-prefix")+path+id, http.StatusSeeOther)

}

// uploadFile uploads a single file to GCS.
func uploadFile(object string, c cli.Context) error {
	fmt.Printf("attempting to upload %s to GCS\n", object)

	ctx := context.Background()

	// open connection with GCS
	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Println("Failed to create client:", err)
	}
	defer client.Close()

	// open temp file
	f, err := os.Open(fmt.Sprintf("./tmp%s", object))
	if err != nil {
		fmt.Println("error opening file:", err)
		return fmt.Errorf("os.Open: %v", err)
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// remove slash from beginning of string as it's not a real dir
	exists := checkFile(object, c)
	if exists {
		return fmt.Errorf("file already exists: %v", object)
	}

	o := client.Bucket(c.String("bucket-name")).Object(strings.TrimPrefix(object, "/"))

	// Upload an object with storage.Writer.
	wc := o.NewWriter(ctx)
	if _, err = io.Copy(wc, f); err != nil {
		fmt.Println("error copying file:", err)
		return fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		fmt.Println("error closing writer:", err)
		return fmt.Errorf("Writer.Close: %v", err)
	}
	fmt.Printf("Blob %v uploaded to GCS.\n", object)
	return nil
}

// serveFile serves a single file from GCS.
func serveFile(w http.ResponseWriter, path string, c cli.Context) {
	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Printf("failed to create client: %v", err)
	}
	defer client.Close()

	o := client.Bucket(c.String("bucket-name")).Object(path[1:])
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

func checkFile(path string, c cli.Context) bool {
	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Printf("failed to create client: %v", err)
	}
	defer client.Close()

	o := client.Bucket(c.String("bucket-name")).Object(path[1:])
	if err != nil {
		fmt.Println("error getting object from GCS:", err)
		return false
	}
	ot := o.ReadCompressed(true)
	rc, err := ot.NewReader(ctx)
	if err != nil {
		fmt.Println("error reading file from GCS:", err)
		return false
	}
	defer rc.Close()

	return true

}

// get raw data of a "dir" from GCS
func getFiles(prefix string, delim string, c cli.Context) []GCSObject {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Println("error opening storage client", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*100)
	defer cancel()

	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: delim,
	}

	it := client.Bucket(c.String("bucket-name")).Objects(ctx, query)

	objs := []GCSObject{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Printf("Bucket(%q).Objects(): %v", c.String("bucket-name"), err)
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

// handleSearch searches the GCS bucket and returns the results.
func handleSearch(w http.ResponseWriter, r *http.Request, c cli.Context) {

	query := r.URL.Query().Get("q")
	if query != "" {
		objects := getFiles("", "", c)

		results := objects[:0]

		// loop through all the bucket objects and check if the name contains the query
		// if it does, build a new slice and add the item
		for _, item := range objects {
			if strings.Contains(item.Name, query) {
				results = append(results, item)
			}
		}

		// get list of formatted objects for final output
		m := buildResponse(results, "/search", r.URL.Query().Get("id"), c)

		// send data to html template
		renderEmbeddedTemplate(w, m)
	}
}

// buildResponse builds the response that will be sent to the template.
func buildResponse(objects []GCSObject, path, id string, c cli.Context) *response {
	Files := []Object{}
	Dirs := []Object{}
	Paths := []Object{}

	// build template compatible map
	for _, item := range objects {

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

	response := &response{
		Files:        Files,
		Dirs:         Dirs,
		Paths:        Paths,
		Current:      path,
		Id:           id,
		BucketName:   c.String("bucket-name"),
		PathPrefix:   c.String("path-prefix"),
		DomainPrefix: c.String("domain-prefix"),
		AllowUpload:  c.Bool("allow-upload"),
		AllowDelete:  c.Bool("allow-delete"),
		AllowSearch:  c.Bool("allow-search"),
	}

	return response
}

// size converts raw size from GCS to KB.
func size(s int64) float64 {
	return math.Round(float64(s) * .001)
}

// renderEmbeddedTemplate renders a template based on the given data.
func renderEmbeddedTemplate(w http.ResponseWriter, v *response) {
	t, err := template.ParseFS(templatesDir, "templates/*.html")
	if err != nil {
		fmt.Println("error parsing embedded template dir:", err)
	}

	err = t.ExecuteTemplate(w, "layout", v)
	if err != nil {
		fmt.Println("error executing tempate:", err)
	}

}
