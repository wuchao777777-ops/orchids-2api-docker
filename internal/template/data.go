package template

// PageData represents the data passed to page templates
type PageData struct {
	Title     string
	AdminPath string
	ActiveTab string
	Stats     *Stats
}

// Stats represents statistics data
type Stats struct {
	TotalAccounts    int
	NormalAccounts   int
	AbnormalAccounts int
}
