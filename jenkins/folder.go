package jenkins

import (
	"encoding/xml"
	"fmt"
	"strings"
)

type folder struct {
	XMLName       xml.Name         `xml:"com.cloudbees.hudson.plugins.folder.Folder"`
	Description   string           `xml:"description"`
	DisplayName   string           `xml:"displayName,omitempty"`
	Properties    folderProperties `xml:"properties"`
	FolderViews   xmlRawProperty   `xml:"folderViews"`
	HealthMetrics xmlRawProperty   `xml:"healthMetrics"`
}

type folderProperties struct {
	Security *folderSecurity  `xml:"com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty,omitempty"`
	Other    []xmlRawProperty `xml:",any"`
}

type folderSecurity struct {
	InheritanceStrategy folderPermissionInheritanceStrategy `xml:"inheritanceStrategy"`
	Permission          []string                            `xml:"permission"`
}

type folderPermissionInheritanceStrategy struct {
	Class string `xml:"class,attr"`
}

type xmlRawProperty struct {
	XMLName xml.Name
	Plugin  string `xml:"plugin,attr,omitempty"`
	Raw     string `xml:",innerxml"`
}

func parseFolder(config string) (*folder, error) {
	ret := &folder{}

	doc := handleXml(config)
	if err := xml.Unmarshal(doc, &ret); err != nil {
		return ret, fmt.Errorf("could not parse job XML: %w", err)
	}

	return ret, nil
}

func (j *folder) Render() ([]byte, error) {
	return xml.MarshalIndent(j, "", "\t")
}

func handleXml(def string) []byte {
	// Go's encoding/xml only supports XML 1.0. Jenkins returns XML 1.1
	// declarations in some responses (e.g. <?xml version="1.1" encoding="UTF-8"?>).
	// The XML 1.1 additions Jenkins actually uses are backwards-compatible with
	// 1.0 parsers, so rewriting the version declaration is safe. Both single-
	// and double-quoted attribute variants are replaced.
	//
	// Known issue: if Jenkins config XML ever includes characters outside the
	// XML 1.0 legal set (C0 control chars), parsing will still fail even with
	// this workaround. That is a separate upstream bug.
	def = strings.ReplaceAll(def, "version='1.1'", "version='1.0'")
	def = strings.ReplaceAll(def, `version="1.1"`, `version="1.0"`)
	return []byte(def)
}