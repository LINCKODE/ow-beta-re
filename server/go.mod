module owserver

go 1.27.1

tool google.golang.org/protobuf/cmd/protoc-gen-go

require (
	github.com/ardanlabs/conf/v3 v3.13.0
	google.golang.org/protobuf v1.36.12
)

require github.com/yuin/gopher-lua v1.1.1 // indirect

// The C++ systems' cgo wrappers (systems/README.md "Integration"); build them with `make libs`.
require owecs v0.0.0

replace owecs => ../systems/ecs/go

require owstatescript v0.0.0

replace owstatescript => ../systems/statescript/go

require owcontent v0.0.0

replace owcontent => ../systems/content/go

require owstu v0.0.0

replace owstu => ../systems/stu/go
