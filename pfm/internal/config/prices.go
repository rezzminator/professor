package config

import "path/filepath"

// PricesFileName is the optional model price override, beside FileName.
const PricesFileName = "pfm.prices.json"

// PricesPath is pfm.prices.json beside the given pfm config file.
func PricesPath(pfmConfigPath string) string {
	return filepath.Join(filepath.Dir(pfmConfigPath), PricesFileName)
}
