package sparkplug

// Config holds run configuration metadata about sparkplug.
type Config struct {
	Port     int
	ProxyTo  string
	Endpoint string
}
