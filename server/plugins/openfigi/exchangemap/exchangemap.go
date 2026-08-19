// Package exchangemap provides bidirectional OpenFIGI exchange code <-> ISO MIC lookups.
// The mapping is built from a generated Go literal (codes_generated.go) and is
// read-only after construction, safe for concurrent use without a mutex.
package exchangemap

// ExchangeMap provides bidirectional OpenFIGI code <-> MIC lookups.
//
// OpenFIGI reports an exchange code from either of two namespaces: a venue code
// naming one exchange, or a composite code naming the group of venues a listing
// spans. They are held apart because 142 codes appear in both and six of those
// denote different exchanges in each, so one lookup cannot serve both.
type ExchangeMap struct {
	micToCode        map[string]string
	codeToMICs       map[string][]string
	compositeCountry map[string]string
	micCountry       map[string]string
}

// New builds an ExchangeMap from the generated codes variable.
func New() *ExchangeMap {
	m := &ExchangeMap{
		micToCode:        make(map[string]string, len(codes)*2),
		codeToMICs:       codes,
		compositeCountry: composites,
		micCountry:       micCountry,
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

// CompositeCountry returns the ISO 3166 country an OpenFIGI composite code
// spans, or "" when the code names no composite this can place. A composite is
// a market rather than a venue, so a listing reported under one has no single
// operating MIC; the country is what remains knowable about it.
func (m *ExchangeMap) CompositeCountry(code string) string {
	return m.compositeCountry[code]
}

// MICCountry returns the ISO 3166 country an operating MIC is in, or "" when
// the MIC is not known. It is what lets a composite's market be compared with a
// venue another provider named.
func (m *ExchangeMap) MICCountry(mic string) string {
	return m.micCountry[mic]
}

// MICToExchCode returns the OpenFIGI exchange code for an ISO 10383 MIC.
// Composites are excluded: a MIC maps back to the venue that is it, never to a
// group it belongs to.
func (m *ExchangeMap) MICToExchCode(mic string) (string, bool) {
	code, ok := m.micToCode[mic]
	return code, ok
}
