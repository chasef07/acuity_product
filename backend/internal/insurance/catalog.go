package insurance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
)

type Catalog struct {
	Carriers []Carrier `json:"carriers"`
	Regions  []Region  `json:"regions"`
}

type Carrier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Region struct {
	ID     string        `json:"id"`
	Plans  []RegionPlan  `json:"plans"`
	Tables []OfficeTable `json:"tables"`
}

type RegionPlan struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Family      string   `json:"family"`
	Coverage    string   `json:"coverage"`
	CarrierCode string   `json:"carrierCode"`
	CarrierID   string   `json:"carrierId"`
	SelfPay     bool     `json:"selfPay"`
	Aliases     []string `json:"aliases"`
}

type OfficeTable struct {
	ID      string     `json:"id"`
	Offices []string   `json:"offices"`
	Doctors []string   `json:"doctors"`
	Rows    []TableRow `json:"rows"`
}

type TableRow struct {
	PlanID      string            `json:"planId"`
	Doctors     map[string]string `json:"doctors"`
	Requires    []Requirement     `json:"requires"`
	OnlyOffices []string          `json:"onlyOffices"`
	Notice      string            `json:"notice"`
	Note        string            `json:"note"`
}

type Requirement struct {
	Kind    string `json:"kind"`
	Channel string `json:"channel"`
}

func ParseCatalog(body []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("%w: insurance catalog JSON: %w", ErrInvalidInput, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Catalog{}, fmt.Errorf("%w: insurance catalog must be one JSON object", ErrInvalidInput)
	}
	return catalog, nil
}

func (c Catalog) encode() []byte {
	body, _ := json.Marshal(c)
	return body
}

func contentHash(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func TouchedOffices(before, after Catalog) []string {
	offices := append(before.offices(), after.offices()...)
	slices.Sort(offices)
	touched := []string{}
	for _, office := range slices.Compact(offices) {
		if !reflect.DeepEqual(before.officeView(office), after.officeView(office)) {
			touched = append(touched, office)
		}
	}
	return touched
}

func (c Catalog) offices() []string {
	offices := []string{}
	for _, region := range c.Regions {
		for _, table := range region.Tables {
			offices = append(offices, table.Offices...)
		}
	}
	return offices
}

type officeView struct {
	carriers []Carrier
	regions  []Region
}

func (c Catalog) officeView(office string) officeView {
	view := officeView{carriers: c.Carriers}
	for _, region := range c.Regions {
		tables := slices.DeleteFunc(slices.Clone(region.Tables), func(table OfficeTable) bool {
			return !slices.Contains(table.Offices, office)
		})
		if len(tables) > 0 {
			view.regions = append(view.regions, Region{ID: region.ID, Plans: region.Plans, Tables: tables})
		}
	}
	return view
}

func (c Catalog) tableOffices(office string) []string {
	offices := []string{office}
	for _, region := range c.Regions {
		for _, table := range region.Tables {
			if slices.Contains(table.Offices, office) {
				offices = append(offices, table.Offices...)
			}
		}
	}
	slices.Sort(offices)
	return slices.Compact(offices)
}
