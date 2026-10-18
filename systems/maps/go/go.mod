module owmaps

go 1.27

// Tests read the install through the content wrapper (systems/content/go).
require owcontent v0.0.0

require owstu v0.0.0 // indirect

replace (
	owcontent => ../../content/go
	owstu => ../../stu/go
)
