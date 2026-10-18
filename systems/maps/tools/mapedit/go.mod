module mapedit

go 1.27

require (
	owcontent v0.0.0
	owmaps v0.0.0
	owstu v0.0.0
)

replace (
	owcontent => ../../../content/go
	owmaps => ../../go
	owstu => ../../../stu/go
)
