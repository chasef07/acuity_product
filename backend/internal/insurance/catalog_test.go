package insurance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

func syntheticCatalog() insurance.Catalog {
	row := func(plan string) insurance.TableRow {
		return insurance.TableRow{PlanID: plan, Doctors: map[string]string{"Dr. Example": "yes"}, Requires: []insurance.Requirement{}, OnlyOffices: []string{}}
	}
	plan := func(id string) insurance.RegionPlan {
		return insurance.RegionPlan{ID: id, Label: "Synthetic " + id, Family: "Synthetic", Coverage: "medical", CarrierCode: "SYN01", CarrierID: "car00001", Aliases: []string{"Synthetic alias"}}
	}
	return insurance.Catalog{
		Carriers: []insurance.Carrier{{ID: "car00001", Name: "SYNTHETIC CARRIER"}},
		Regions: []insurance.Region{
			{ID: "south_florida", Plans: []insurance.RegionPlan{plan("south-hmo")}, Tables: []insurance.OfficeTable{
				{ID: "doctors", Offices: []string{"hollywood", "sweetwater", "north_miami_beach_optical"}, Doctors: []string{"Dr. Example"}, Rows: []insurance.TableRow{row("south-hmo")}},
			}},
			{ID: "spring_hill_crystal_river", Plans: []insurance.RegionPlan{plan("north-hmo"), plan("north-ppo")}, Tables: []insurance.OfficeTable{
				{ID: "crystal_river", Offices: []string{"crystal_river"}, Doctors: []string{"Dr. Example"}, Rows: []insurance.TableRow{row("north-hmo")}},
				{ID: "spring_hill", Offices: []string{"spring_hill"}, Doctors: []string{"Dr. Example"}, Rows: []insurance.TableRow{row("north-hmo"), row("north-ppo")}},
			}},
		},
	}
}

func TestTouchedOfficesFollowWhatEachOfficeServes(t *testing.T) {
	everyOffice := []string{"crystal_river", "hollywood", "north_miami_beach_optical", "spring_hill", "sweetwater"}
	for _, tc := range []struct {
		name   string
		change func(*insurance.Catalog)
		want   []string
	}{
		{"no change touches nothing", func(*insurance.Catalog) {}, []string{}},
		{"a shared Spring Hill and Crystal River plan row touches both offices", func(c *insurance.Catalog) {
			c.Regions[1].Plans[0].Label = "Renamed HMO"
		}, []string{"crystal_river", "spring_hill"}},
		{"a shared South Florida plan alias touches every South Florida office", func(c *insurance.Catalog) {
			c.Regions[0].Plans[0].Aliases = append(c.Regions[0].Plans[0].Aliases, "Another alias")
		}, []string{"hollywood", "north_miami_beach_optical", "sweetwater"}},
		{"removing a plan from a region touches every office of that region", func(c *insurance.Catalog) {
			c.Regions[1].Plans = c.Regions[1].Plans[:1]
		}, []string{"crystal_river", "spring_hill"}},
		{"a Crystal River table row touches only Crystal River", func(c *insurance.Catalog) {
			c.Regions[1].Tables[0].Rows[0].Doctors["Dr. Example"] = "pending"
		}, []string{"crystal_river"}},
		{"a Spring Hill table row note touches only Spring Hill", func(c *insurance.Catalog) {
			c.Regions[1].Tables[1].Rows[1].Note = "Referral from PCP"
		}, []string{"spring_hill"}},
		{"a doctor column on the shared South Florida table touches its three offices", func(c *insurance.Catalog) {
			c.Regions[0].Tables[0].Doctors = append(c.Regions[0].Tables[0].Doctors, "Dr. New")
		}, []string{"hollywood", "north_miami_beach_optical", "sweetwater"}},
		{"a carrier change touches every office", func(c *insurance.Catalog) {
			c.Carriers[0].Name = "RENAMED CARRIER"
		}, everyOffice},
		{"adding a carrier touches every office", func(c *insurance.Catalog) {
			c.Carriers = append(c.Carriers, insurance.Carrier{ID: "car00002", Name: "SECOND CARRIER"})
		}, everyOffice},
		{"adding an office to a table touches it and the offices it joins", func(c *insurance.Catalog) {
			c.Regions[1].Tables[0].Offices = append(c.Regions[1].Tables[0].Offices, "inverness")
		}, []string{"crystal_river", "inverness"}},
		{"dropping an office from every table touches it and its former table", func(c *insurance.Catalog) {
			c.Regions[0].Tables[0].Offices = []string{"hollywood", "sweetwater"}
		}, []string{"hollywood", "north_miami_beach_optical", "sweetwater"}},
		{"reordering regions changes no office", func(c *insurance.Catalog) {
			c.Regions[0], c.Regions[1] = c.Regions[1], c.Regions[0]
		}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := syntheticCatalog()
			tc.change(&after)
			if got := insurance.TouchedOffices(syntheticCatalog(), after); !slices.Equal(got, tc.want) {
				t.Fatalf("touched = %v, want %v", got, tc.want)
			}
			if got := insurance.TouchedOffices(after, syntheticCatalog()); !slices.Equal(got, tc.want) {
				t.Fatalf("reverting touched = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCatalogRoundTripsTheMiddlewareExportByteForByte(t *testing.T) {
	export, err := os.ReadFile("testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := insurance.ParseCatalog(export)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.TrimSuffix(export, []byte("\n")); !bytes.Equal(encoded, want) {
		t.Fatalf("re-encoded export differs: %d bytes, want %d", len(encoded), len(want))
	}
	if len(catalog.Carriers) != 50 || len(catalog.Regions) != 2 || len(catalog.Regions[1].Tables) != 2 {
		t.Fatalf("export shape: %d carriers, %d regions", len(catalog.Carriers), len(catalog.Regions))
	}
}

func TestCatalogParsingRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	for _, body := range []string{
		`{"carriers":[],"regions":[],"plans":[]}`,
		`{"carriers":[{"id":"car1","name":"A","code":"X"}],"regions":[]}`,
		`{"carriers":[],"regions":[{"id":"r","plans":[],"tables":[{"id":"t","offices":[],"doctors":[],"rows":[{"planId":"p","doctors":{},"requires":[],"onlyOffices":[],"notice":"","note":"","patientId":"x"}]}]}]}`,
		`{"carriers":[],"regions":[]} {}`,
		`not json`,
	} {
		if _, err := insurance.ParseCatalog([]byte(body)); !errors.Is(err, insurance.ErrInvalidInput) {
			t.Errorf("ParseCatalog(%s) error = %v, want invalid input", body, err)
		}
	}
}
