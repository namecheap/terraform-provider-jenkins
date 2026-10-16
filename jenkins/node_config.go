package jenkins

import (
	"encoding/xml"
	"fmt"
)

// Element and class names Jenkins core and the job-restrictions plugin write
// in a node's config.xml.
const (
	jnlpLauncherClass         = "hudson.slaves.JNLPLauncher"
	jobRestrictionPropertyTag = "com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty"
	regexNameRestrictionClass = "com.synopsys.arc.jenkinsci.plugins.jobrestrictions.restrictions.job.RegexNameRestriction"
)

// nodeConfig is a node's config.xml. The fields the resource manages are
// typed. Every other element is kept raw, so an update writes back what it
// does not manage unchanged. XMLName keeps the root element, which varies by
// node type (<slave> for a permanent agent). Jenkins reads the elements in
// any order.
type nodeConfig struct {
	XMLName        xml.Name
	Attrs          []xml.Attr       `xml:",any,attr"`
	Name           string           `xml:"name"`
	Description    string           `xml:"description"`
	RemoteFS       string           `xml:"remoteFS"`
	NumExecutors   int64            `xml:"numExecutors"`
	Mode           string           `xml:"mode"`
	Other          []xmlRawProperty `xml:",any"`
	Launcher       *nodeLauncher    `xml:"launcher"`
	Label          string           `xml:"label"`
	NodeProperties nodeProperties   `xml:"nodeProperties"`
}

// nodeLauncher is the <launcher> element. Only the WebSocket flag of the
// inbound launcher is managed. Its other settings (workDirSettings, tunnel)
// and any other launcher type are written back as read.
type nodeLauncher struct {
	Class     string           `xml:"class,attr"`
	Attrs     []xml.Attr       `xml:",any,attr"`
	Other     []xmlRawProperty `xml:",any"`
	WebSocket *bool            `xml:"webSocket"`
}

type nodeProperties struct {
	Properties []xmlRawProperty `xml:",any"`
}

// jobRestrictionXML is the jobRestriction element of a JobRestrictionProperty.
type jobRestrictionXML struct {
	XMLName        xml.Name `xml:"jobRestriction"`
	Class          string   `xml:"class,attr"`
	Regex          string   `xml:"regexExpression"`
	CheckShortName bool     `xml:"checkShortName"`
}

// jobRestriction is a RegexNameRestriction, the one restriction type the
// resource manages.
type jobRestriction struct {
	Regex          string
	CheckShortName bool
}

// mode returns the node usage, falling back to NORMAL (Jenkins' own default)
// when config.xml has no <mode> element, so Read agrees with the schema
// default instead of planning a change.
func (c nodeConfig) mode() string {
	if c.Mode == "" {
		return "NORMAL"
	}
	return c.Mode
}

// webSocket reports whether the node is an inbound agent set to connect over
// WebSocket. Other launcher types have no such setting and report false.
func (c nodeConfig) webSocket() bool {
	l := c.Launcher
	return l != nil && l.Class == jnlpLauncherClass && l.WebSocket != nil && *l.WebSocket
}

// setWebSocket sets the WebSocket flag of the inbound launcher. Other launcher
// types are left alone when the flag is off, and refused when it is on:
// XStream would fail on a field their class does not have.
func (c *nodeConfig) setWebSocket(on bool) error {
	if c.Launcher == nil || c.Launcher.Class != jnlpLauncherClass {
		if on {
			return fmt.Errorf("web_socket applies to inbound agents only, and node %q is not launched by %s", c.Name, jnlpLauncherClass)
		}
		return nil
	}
	c.Launcher.WebSocket = &on
	return nil
}

// findJobRestriction returns the position of the JobRestrictionProperty in
// the node properties and its restriction, or -1. Jenkins keeps at most one
// property per type.
func (c nodeConfig) findJobRestriction() (int, jobRestrictionXML) {
	for i, p := range c.NodeProperties.Properties {
		if p.XMLName.Local != jobRestrictionPropertyTag {
			continue
		}
		var prop struct {
			Restriction jobRestrictionXML `xml:"jobRestriction"`
		}
		// An undecodable restriction is reported with an empty class, as a
		// type the resource does not manage.
		_ = xml.Unmarshal([]byte("<p>"+p.Raw+"</p>"), &prop)
		return i, prop.Restriction
	}
	return -1, jobRestrictionXML{}
}

// jobRestriction returns the node's regex restriction. A restriction of
// another type, set in the UI, has no place in the schema and reads as nil.
func (c nodeConfig) jobRestriction() *jobRestriction {
	i, r := c.findJobRestriction()
	if i < 0 || r.Class != regexNameRestrictionClass {
		return nil
	}
	return &jobRestriction{Regex: r.Regex, CheckShortName: r.CheckShortName}
}

// setJobRestriction makes r the node's restriction. A nil r removes a regex
// restriction and leaves a restriction of another type in place, since the
// resource cannot read it back.
func (c *nodeConfig) setJobRestriction(r *jobRestriction) error {
	i, cur := c.findJobRestriction()
	props := c.NodeProperties.Properties
	if r == nil {
		if i >= 0 && cur.Class == regexNameRestrictionClass {
			c.NodeProperties.Properties = append(props[:i:i], props[i+1:]...)
		}
		return nil
	}

	inner, err := xml.Marshal(jobRestrictionXML{Class: regexNameRestrictionClass, Regex: r.Regex, CheckShortName: r.CheckShortName})
	if err != nil {
		return err
	}
	prop := xmlRawProperty{XMLName: xml.Name{Local: jobRestrictionPropertyTag}, Raw: string(inner)}
	if i >= 0 {
		props[i] = prop
	} else {
		c.NodeProperties.Properties = append(props, prop)
	}
	return nil
}

// apply sets the managed fields to the planned values of m.
func (c *nodeConfig) apply(m nodeResourceModel) error {
	c.Description = m.Description.ValueString()
	c.RemoteFS = m.RemoteFS.ValueString()
	c.NumExecutors = m.NumExecutors.ValueInt64()
	c.Label = m.Labels.ValueString()
	c.Mode = m.Mode.ValueString()
	if err := c.setWebSocket(m.WebSocket.ValueBool()); err != nil {
		return err
	}

	var r *jobRestriction
	if m.JobRestriction != nil {
		r = &jobRestriction{Regex: m.JobRestriction.Regex.ValueString(), CheckShortName: m.JobRestriction.CheckShortName.ValueBool()}
	}
	return c.setJobRestriction(r)
}

func (c nodeConfig) render() ([]byte, error) {
	return xml.Marshal(c)
}
