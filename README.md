[![Build Status](https://ci.rmilo.dev/api/badges/robbymilo/object-storage-ui/status.svg)](https://ci.rmilo.dev/robbymilo/object-storage-ui)

# Object Storage UI (WIP)

A browser interface for GCS.

To run:

- `export GOOGLE_APPLICATION_CREDENTIALS="/path/to/key.json"`
- `go run main.go --bucket-name {gcs-bucket-name} --allow-upload`
- visit http://localhost:3000, or visit http://localhost:3000?json=true for a json view

```
NAME:
   object-storage-ui - A browser interface for Google Cloud Storage (GCS).

USAGE:
   object-storage-ui [global options] command [command options]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --bucket-name value    The name of the bucket to serve.
   --path-prefix value    A path to prefix the application. Use if running the application on a subdirectory.
   --domain-prefix value  A domain to use for serving files. Use if files will be served from different application.
   --allow-upload         Allow files to be uploaded. (default: false)
   --allow-delete         Allow files to be deleted. (default: false)
   --allow-search         Allow files to be search. (default: true)
                          Location of the service account json key. [$GOOGLE_APPLICATION_CREDENTIALS]
   --help, -h             show help
```

![object storage ui screenshot](./object-storage-ui.png)

Todo:

- [ ] Add tests
- [ ] Add UI for deleting objects
- [ ] Add support for S3-compatible object stores
