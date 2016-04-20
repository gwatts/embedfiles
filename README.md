# Embed files into Go source code

The embedfiles command converts one or more files into Go source code so that
they may be compiled directly into a Go program.

It is intended to be run via  go generate tool and creates an instance
variable that provides file-like access to the embedded assets using
a bytes.Reader.

Additionally each file instance complies with the http.File interface.

By default the generated package does not provide a type compatible with the
http.FileServer interface to avoid importing net/http - Supplying the
-include-http flag will enable support for that interface.

For example to embed the html and css files in an assets directory into
a new file called assets.go as part of a package called webserver:

```bash
embedfiles -filename assets.go -package webserver -include-http -var Assets assets/*.html assets/*.css
```

Code within the assets package could then open index.html for read:

```go
f, err := Assets.Open("assets/index.html")
```

or could use an instance as a file server (assuming the -include-http flag was set):

```go
log.Fatal(http.ListenAndServe(":8080", http.FileServer(Assets))
```

As each call to embedfiles generates a completely self-contained .go file,
multiple independent .go files can be generated and compiled into a single
package by using different -varname options, allowing for discrete groups
of files to be assigned to different variable names.


## Usage

```
  Usage:  embedfiles [arguments] <file glob> [<file glob> ...]

  Arguments:

    -filename string
          File to write go output to.  Defaults to stdout (default "-")
    -include-http
          If true then the generated file will import net/http and comply with the http.FileSystem interface
    -package string
          Package name to use for output file. (default "main")
    -var string
          Variable name to assign the assets to.  Start with a capital letter to export from the package (default "assets")
```

## Usage with go generate

Add one or more lines to existing .go source code to trigger embedfiles when `go generate`
is executed:

```go
//go:generate embedfiles -filename email_templates.go templates/*.txt
```

(note the lack of spaces between `//` and `go:generate` there!)

See the [go generate blog post](https://blog.golang.org/generate) or [documentation](https://golang.org/cmd/go/#hdr-Generate_Go_files_by_processing_source) 
for more details
