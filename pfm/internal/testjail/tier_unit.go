//go:build !jail && !e2e && !journey

package testjail

// unitBinary: this test binary was built without the jail tag (nor the e2e or journey suite's), so it
// compiles no test that may touch the machine. It is the unit tier: once the unit net is armed
// (unitnet.go) it runs on any host, inside that net.
const unitBinary = true
