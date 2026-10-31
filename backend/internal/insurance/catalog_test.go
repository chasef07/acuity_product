package insurance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

func syntheticCatalog() insurance.Catalog {
	row := func(plan string) insurance.TableRow {
		return insurance.TableRow{PlanID: plan, Doctors: map[string]string{"Dr. Example": "yes"}, BeforeBooking: "none", OnlyOffices: []string{}}
	}
	plan := func(id string) insurance.RegionPlan {
		return insurance.RegionPlan{ID: id, Label: "Synthetic " + id, Family: "Synthetic", Coverage: "medical", CarrierID: "car00001", Aliases: []string{"Synthetic alias"}}
	}
	return insurance.Catalog{
		Carriers: []insurance.Carrier{{ID: "car00001", Code: "SYN01", Name: "SYNTHETIC CARRIER"}},
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

func TestOfficeNamesJoinEveryLocationRoutedToTheOfficeAndAnOfficeWithNoLocationShowsItsID(t *testing.T) {
	routes := []access.AbitaOfficeRoute{
		{OfficeKey: "sweetwater-optical", LocationID: "location-2", LocationName: "Sweetwater Optical"},
		{OfficeKey: "sweetwater", LocationID: "location-1", LocationName: "Sweetwater"},
		{OfficeKey: "hollywood", LocationID: "location-3", LocationName: "Hollywood"},
		{OfficeKey: "north-miami-beach-optical", LocationID: "location-4", LocationName: "North Miami Beach Optical"},
		{OfficeKey: "spring-hill", LocationID: "location-5", LocationName: "Spring Hill"},
		{OfficeKey: "demo-office", LocationID: "location-6", LocationName: "Demo"},
	}
	want := map[string]string{
		"hollywood":                 "Hollywood",
		"sweetwater":                "Sweetwater / Sweetwater Optical",
		"north_miami_beach_optical": "North Miami Beach Optical",
		"spring_hill":               "Spring Hill",
		"crystal_river":             "crystal_river",
	}
	if got := insurance.OfficeNames(syntheticCatalog(), routes); !reflect.DeepEqual(got, want) {
		t.Fatalf("office names = %#v, want %#v", got, want)
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
		{"moving a plan both northern tables list to another carrier touches both offices", func(c *insurance.Catalog) {
			c.Regions[1].Plans[0].CarrierID = "car00002"
		}, []string{"crystal_river", "spring_hill"}},
		{"removing a plan only Spring Hill lists touches only Spring Hill", func(c *insurance.Catalog) {
			c.Regions[1].Plans = c.Regions[1].Plans[:1]
			c.Regions[1].Tables[1].Rows = c.Regions[1].Tables[1].Rows[:1]
		}, []string{"spring_hill"}},
		{"adding a plan to the Spring Hill table touches only Spring Hill", func(c *insurance.Catalog) {
			c.Regions[1].Plans = append(c.Regions[1].Plans, insurance.RegionPlan{ID: "north-epo", Label: "Synthetic north-epo", Family: "Synthetic", Coverage: "medical", Aliases: []string{}})
			c.Regions[1].Tables[1].Rows = append(c.Regions[1].Tables[1].Rows, insurance.TableRow{PlanID: "north-epo", Doctors: map[string]string{"Dr. Example": "yes"}, BeforeBooking: "none", OnlyOffices: []string{}})
		}, []string{"spring_hill"}},
		{"renaming a plan only Spring Hill lists touches only Spring Hill", func(c *insurance.Catalog) {
			c.Regions[1].Plans[1].Label = "Renamed PPO"
		}, []string{"spring_hill"}},
		{"a region plan no table lists touches no office", func(c *insurance.Catalog) {
			c.Regions[1].Plans = append(c.Regions[1].Plans, insurance.RegionPlan{ID: "north-unlisted", Aliases: []string{}})
		}, []string{}},
		{"a Crystal River table row touches only Crystal River", func(c *insurance.Catalog) {
			c.Regions[1].Tables[0].Rows[0].Doctors["Dr. Example"] = "pending"
		}, []string{"crystal_river"}},
		{"a Spring Hill table row's before-booking step touches only Spring Hill", func(c *insurance.Catalog) {
			c.Regions[1].Tables[1].Rows[1].BeforeBooking = "pcp_referral"
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

func TestCatalogRoundTripsTheV2ExportByteForByte(t *testing.T) {
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
		`{"carriers":[],"regions":[{"id":"r","plans":[{"id":"p","label":"P","family":"F","coverage":"medical","carrierCode":"X","carrierId":"","selfPay":false,"aliases":[]}],"tables":[]}]}`,
		`{"carriers":[],"regions":[{"id":"r","plans":[],"tables":[{"id":"t","offices":[],"doctors":[],"rows":[{"planId":"p","doctors":{},"requires":[],"onlyOffices":[],"notice":"","note":""}]}]}]}`,
		`{"carriers":[],"regions":[{"id":"r","plans":[],"tables":[{"id":"t","offices":[],"doctors":[],"rows":[{"planId":"p","doctors":{},"beforeBooking":"none","onlyOffices":[],"notice":"","patientId":"x"}]}]}]}`,
		`{"carriers":[],"regions":[]} {}`,
		`not json`,
	} {
		if _, err := insurance.ParseCatalog([]byte(body)); !errors.Is(err, insurance.ErrInvalidInput) {
			t.Errorf("ParseCatalog(%s) error = %v, want invalid input", body, err)
		}
	}
}
