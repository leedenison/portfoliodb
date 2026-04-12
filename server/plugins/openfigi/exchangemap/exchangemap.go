// Package exchangemap provides bidirectional OpenFIGI exchange code <-> ISO MIC lookups.
// The mapping is built from a generated Go literal (codes_generated.go) and is
// read-only after construction, safe for concurrent use without a mutex.
package exchangemap

// ExchangeMap provides bidirectional OpenFIGI code <-> MIC lookups.
type ExchangeMap struct {
	micToCode  map[string]string
	codeToMICs map[string][]string
}

// New builds an ExchangeMap from the generated codes variable.
func New() *ExchangeMap {
	m := &ExchangeMap{
		micToCode:  make(map[string]string, len(codes)*2),
		codeToMICs: codes,
	}
	for code, mics := range codes {
		for _, mic := range mics {
			m.micToCode[mic] = code
		}
	}
	return m
}

// ExchCodeToMICs returns the operating MIC(s) for an OpenFIGI equity exchange code.
func (m *ExchangeMap) ExchCodeToMICs(code string) []string {
	return m.codeToMICs[code]
}

// MICToExchCode returns the OpenFIGI exchange code for an ISO 10383 MIC.
func (m *ExchangeMap) MICToExchCode(mic string) (string, bool) {
	code, ok := m.micToCode[mic]
	return code, ok
}
