// Package junit writes JUnit XML, the format CircleCI's Tests tab reads.
package junit

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"time"
)

// Suite is one test suite.
type Suite struct {
	Name  string
	Cases []Case
}

// Case is one test case. A non-empty Failure fails it.
type Case struct {
	Name      string
	ClassName string
	Time      time.Duration
	Failure   string
	Output    string
}

type xmlSuites struct {
	XMLName xml.Name   `xml:"testsuites"`
	Suites  []xmlSuite `xml:"testsuite"`
}

type xmlSuite struct {
	Name     string    `xml:"name,attr"`
	Tests    int       `xml:"tests,attr"`
	Failures int       `xml:"failures,attr"`
	Time     float64   `xml:"time,attr"`
	Cases    []xmlCase `xml:"testcase"`
}

type xmlCase struct {
	Name      string      `xml:"name,attr"`
	ClassName string      `xml:"classname,attr"`
	Time      float64     `xml:"time,attr"`
	Failure   *xmlFailure `xml:"failure,omitempty"`
	SystemOut string      `xml:"system-out,omitempty"`
}

type xmlFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// Write writes the suite to path, making its directory.
func (s Suite) Write(path string) error {
	out := xmlSuite{Name: s.Name, Tests: len(s.Cases)}
	for _, c := range s.Cases {
		xc := xmlCase{Name: c.Name, ClassName: c.ClassName, Time: c.Time.Seconds(), SystemOut: c.Output}
		if c.Failure != "" {
			xc.Failure = &xmlFailure{Message: c.Failure, Text: c.Failure}
			out.Failures++
		}
		out.Time += c.Time.Seconds()
		out.Cases = append(out.Cases, xc)
	}
	b, err := xml.MarshalIndent(xmlSuites{Suites: []xmlSuite{out}}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), append(b, '\n')...), 0o600)
}
