module iotsim

go 1.24.7

require (
	github.com/vishvananda/netlink v1.3.0
	github.com/vishvananda/netns v0.0.4
)

require golang.org/x/sys v0.10.0 // indirect

replace golang.org/x/sys => github.com/golang/sys v0.30.0
