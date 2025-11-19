module github.com/puerco/hello

go 1.19

require github.com/sirupsen/logrus v1.8.0

replace github.com/sirupsen/logrus => github.com/sirupsen/logrus v1.3.0

require (
	github.com/magefile/mage v1.10.0 // indirect
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
)
