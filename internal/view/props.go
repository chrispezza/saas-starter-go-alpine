package view

// BaseProps holds data needed by the base layout component.
type BaseProps struct {
	Title       string
	CurrentYear int
	UserName    string // display name shown in the authenticated user menu; empty for guest pages
	// Description is the page-specific meta/og description; blank falls
	// back to DefaultDescription (see MetaDescription).
	Description string
}

// MetaDescription returns the description the layout should advertise.
func (p BaseProps) MetaDescription() string {
	if p.Description != "" {
		return p.Description
	}
	return DefaultDescription
}

// NewBaseProps constructs a BaseProps with CurrentYear computed automatically.
func NewBaseProps(title string) BaseProps {
	return BaseProps{
		Title:       title,
		CurrentYear: CurrentYear(),
	}
}
