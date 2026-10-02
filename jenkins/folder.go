package jenkins

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// FolderViews and HealthMetrics are pointers so a folder built from scratch
// omits both elements and Jenkins fills in its defaults. An empty
// <folderViews/> has no class attribute, and Jenkins cannot instantiate the
// abstract AbstractFolderViewHolder (#233).
type folder struct {
	XMLName       xml.Name         `xml:"com.cloudbees.hudson.plugins.folder.Folder"`
	Description   string           `xml:"description"`
	DisplayName   string           `xml:"displayName,omitempty"`
	Properties    folderProperties `xml:"properties"`
	FolderViews   *xmlRawProperty  `xml:"folderViews,omitempty"`
	HealthMetrics *xmlRawProperty  `xml:"healthMetrics,omitempty"`
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

// xmlRawProperty carries an element we do not manage through a read-modify-
// write unchanged. All attributes are kept, not just plugin: XStream needs
// class to pick the concrete type of an abstract field such as folderViews.
type xmlRawProperty struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Raw     string     `xml:",innerxml"`
}

// UnmarshalXML drops namespace declarations from Attrs. encoding/xml cannot
// re-marshal them: it emits the element's own xmlns as well, producing a
// duplicate attribute that Jenkins rejects. The encoder declares any
// namespace an element or attribute needs by itself.
func (p *xmlRawProperty) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain xmlRawProperty
	if err := d.DecodeElement((*plain)(p), &start); err != nil {
		return err
	}
	attrs := p.Attrs[:0]
	for _, a := range p.Attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		attrs = append(attrs, a)
	}
	p.Attrs = attrs
	return nil
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
