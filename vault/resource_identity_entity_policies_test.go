package vault

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/hashicorp/vault/api"

	"github.com/hashicorp/terraform-provider-vault/testutil"
	"github.com/hashicorp/terraform-provider-vault/util"
)

func TestAccIdentityEntityPoliciesExclusive(t *testing.T) {
	entity := acctest.RandomWithPrefix("test-entity")
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testutil.TestAccPreCheck(t) },
		Providers:    testProviders,
		CheckDestroy: testAccCheckidentityEntityPoliciesDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccIdentityEntityPoliciesConfigExclusive(entity),
				Check:  testAccIdentityEntityPoliciesCheckAttrs("vault_identity_entity_policies.policies"),
			},
			{
				Config: testAccIdentityEntityPoliciesConfigExclusiveUpdate(entity),
				Check: resource.ComposeTestCheckFunc(
					testAccIdentityEntityPoliciesCheckAttrs("vault_identity_entity_policies.policies"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.policies", "policies.#", "2"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.policies", "policies.0", "dev"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.policies", "policies.1", "test"),
				),
			},
		},
	})
}

func TestAccIdentityEntityPoliciesNonExclusive(t *testing.T) {
	entity := acctest.RandomWithPrefix("test-entity")
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testutil.TestAccPreCheck(t) },
		Providers:    testProviders,
		CheckDestroy: testAccCheckidentityEntityPoliciesDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccIdentityEntityPoliciesConfigNonExclusive(entity),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("vault_identity_entity_policies.dev", "policies.#", "1"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.dev", "policies.0", "dev"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.test", "policies.#", "1"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.test", "policies.0", "test"),
				),
			},
			{
				Config: testAccIdentityEntityPoliciesConfigNonExclusiveUpdate(entity),
				Check: resource.ComposeTestCheckFunc(
					testAccIdentityEntityPoliciesCheckLogical("vault_identity_entity.entity", []string{"dev", "foo"}),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.dev", "policies.#", "1"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.dev", "policies.0", "dev"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.test", "policies.#", "1"),
					resource.TestCheckResourceAttr("vault_identity_entity_policies.test", "policies.0", "foo"),
				),
			},
		},
	})
}

func testAccCheckidentityEntityPoliciesDestroy(s *terraform.State) error {
	client := testProvider.Meta().(*api.Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "vault_identity_entity_policies" {
			continue
		}

		if _, err := readIdentityEntity(client, rs.Primary.ID, false); err != nil {
			if isIdentityNotFoundError(err) {
				continue
			}
			return err
		}

		apiPolicies, err := readIdentityEntityPolicies(client, rs.Primary.ID)
		if err != nil {
			return err
		}
		length := rs.Primary.Attributes["policies.#"]

		if length != "" {
			count, err := strconv.Atoi(length)
			if err != nil {
				return fmt.Errorf("expected %s.# to be a number, got %q", "policies.#", length)
			}

			for i := 0; i < count; i++ {
				resourcePolicy := rs.Primary.Attributes["policies."+strconv.Itoa(i)]
				if found, _ := util.SliceHasElement(apiPolicies, resourcePolicy); found {
					return fmt.Errorf("identity entity %s still has policy %s", rs.Primary.ID, resourcePolicy)
				}
			}
		}
	}
	return nil
}

func testAccIdentityEntityPoliciesCheckAttrs(resource string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		resourceState := s.Modules[0].Resources[resource]
		if resourceState == nil {
			return fmt.Errorf("resource not found in state")
		}

		instanceState := resourceState.Primary
		if instanceState == nil {
			return fmt.Errorf("resource not found in state")
		}

		id := instanceState.ID

		path := identityEntityIDPath(id)
		client := testProvider.Meta().(*api.Client)
		resp, err := client.Logical().Read(path)
		if err != nil {
			return fmt.Errorf("%q doesn't exist", path)
		}

		attrs := map[string]string{
			"entity_id":   "id",
			"entity_name": "name",
			"policies":    "policies",
		}
		for stateAttr, apiAttr := range attrs {
			if resp.Data[apiAttr] == nil && instanceState.Attributes[stateAttr] == "" {
				continue
			}
			var match bool
			switch resp.Data[apiAttr].(type) {
			case json.Number:
				apiData, err := resp.Data[apiAttr].(json.Number).Int64()
				if err != nil {
					return fmt.Errorf("expected API field %s to be an int, was %q", apiAttr, resp.Data[apiAttr])
				}
				stateData, err := strconv.ParseInt(instanceState.Attributes[stateAttr], 10, 64)
				if err != nil {
					return fmt.Errorf("expected state field %s to be an int, was %q", stateAttr, instanceState.Attributes[stateAttr])
				}
				match = apiData == stateData
			case bool:
				if _, ok := resp.Data[apiAttr]; !ok && instanceState.Attributes[stateAttr] == "" {
					match = true
				} else {
					stateData, err := strconv.ParseBool(instanceState.Attributes[stateAttr])
					if err != nil {
						return fmt.Errorf("expected state field %s to be a bool, was %q", stateAttr, instanceState.Attributes[stateAttr])
					}
					match = resp.Data[apiAttr] == stateData
				}
			case []interface{}:
				apiData := resp.Data[apiAttr].([]interface{})
				length := instanceState.Attributes[stateAttr+".#"]
				if length == "" {
					if len(resp.Data[apiAttr].([]interface{})) != 0 {
						return fmt.Errorf("expected state field %s to have %d entries, had 0", stateAttr, len(apiData))
					}
					match = true
				} else {
					count, err := strconv.Atoi(length)
					if err != nil {
						return fmt.Errorf("expected %s.# to be a number, got %q", stateAttr, instanceState.Attributes[stateAttr+".#"])
					}
					if count != len(apiData) {
						return fmt.Errorf("expected %s to have %d entries in state, has %d", stateAttr, len(apiData), count)
					}

					for i := 0; i < count; i++ {
						found := false
						for stateKey, stateValue := range instanceState.Attributes {
							if strings.HasPrefix(stateKey, stateAttr) {
								if apiData[i] == stateValue {
									found = true
								}
							}
						}
						if !found {
							return fmt.Errorf("Expected item %d of %s (%s in state) of %q to be in state but wasn't", i, apiAttr, stateAttr, apiData[i])
						}
					}
					match = true
				}
			default:
				match = resp.Data[apiAttr] == instanceState.Attributes[stateAttr]
			}
			if !match {
				return fmt.Errorf("expected %s (%s in state) of %q to be %q, got %q", apiAttr, stateAttr, path, instanceState.Attributes[stateAttr], resp.Data[apiAttr])
			}
		}
		return nil
	}
}

func testAccIdentityEntityPoliciesCheckLogical(resource string, policies []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		resourceState := s.Modules[0].Resources[resource]
		if resourceState == nil {
			return fmt.Errorf("resource not found in state")
		}

		instanceState := resourceState.Primary
		if instanceState == nil {
			return fmt.Errorf("resource not found in state")
		}

		id := instanceState.ID

		path := identityEntityIDPath(id)
		client := testProvider.Meta().(*api.Client)
		resp, err := client.Logical().Read(path)
		if err != nil {
			return fmt.Errorf("%q doesn't exist", path)
		}

		if resp.Data["policies"] == nil && policies == nil {
			return nil
		}

		apiPolicies := resp.Data["policies"].([]interface{})

		if len(apiPolicies) != len(policies) {
			return fmt.Errorf("expected entity %s to have %d policies, has %d", id, len(policies), len(apiPolicies))
		}

		for _, apiPolicyI := range apiPolicies {
			apiPolicy := apiPolicyI.(string)

			found := false
			for _, policy := range policies {
				if apiPolicy == policy {
					found = true
					break
				}
			}

			if !found {
				return fmt.Errorf("unexpected policy %s in entity %s", apiPolicy, id)
			}
		}

		return nil
	}
}

func testAccIdentityEntityPoliciesConfigExclusive(entity string) string {
	return fmt.Sprintf(`
resource "vault_identity_entity" "entity" {
  name = "%s"
  external_policies = true
}

resource "vault_identity_entity_policies" "policies" {
  entity_id = vault_identity_entity.entity.id
  policies = ["test"]
}`, entity)
}

func testAccIdentityEntityPoliciesConfigExclusiveUpdate(entity string) string {
	return fmt.Sprintf(`
resource "vault_identity_entity" "entity" {
  name = "%s"
  external_policies = true
}

resource "vault_identity_entity_policies" "policies" {
  entity_id = vault_identity_entity.entity.id
  policies = ["dev", "test"]
}`, entity)
}

func testAccIdentityEntityPoliciesConfigNonExclusive(entity string) string {
	return fmt.Sprintf(`
resource "vault_identity_entity" "entity" {
  name = "%s"
  external_policies = true
}

resource "vault_identity_entity_policies" "dev" {
	entity_id = vault_identity_entity.entity.id
  exclusive = false
  policies = ["dev"]
}


resource "vault_identity_entity_policies" "test" {
  entity_id = vault_identity_entity.entity.id
  exclusive = false
  policies = ["test"]
}
`, entity)
}

func testAccIdentityEntityPoliciesConfigNonExclusiveUpdate(entity string) string {
	return fmt.Sprintf(`
resource "vault_identity_entity" "entity" {
  name = "%s"
  external_policies = true
}

resource "vault_identity_entity_policies" "dev" {
	entity_id = vault_identity_entity.entity.id
  exclusive = false
  policies = ["dev"]
}


resource "vault_identity_entity_policies" "test" {
  entity_id = vault_identity_entity.entity.id
  exclusive = false
  policies = ["foo"]
}
`, entity)
}
