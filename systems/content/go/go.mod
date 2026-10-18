module owcontent

go 1.27

// Tests parse what they read with the STU wrapper (systems/stu/go).
require owstu v0.0.0

replace owstu => ../../stu/go
