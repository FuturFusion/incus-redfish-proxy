package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// secureBootIDsKey holds the numbers of the sequential secure boot IDs.
const secureBootIDsKey = redfishConfigPrefix + "secureboot.ids"

// sequentialIDs numbers the entries of all secure boot databases with one counter, like real BMCs do.
// A number is used only once. A deleted entry leaves a gap, a new entry gets the next number.
// The counter restarts when the keys are reset.
type sequentialIDs struct {
	Next int `json:"next"`

	// Numbers maps the database and the content hash of an entry to its number.
	Numbers map[string]int `json:"numbers"`
}

func sequentialIDKey(db secureBootDatabase, hash string) string {
	return db.id + "/" + hash
}

func readSequentialIDs(config map[string]string) (sequentialIDs, error) {
	ids := sequentialIDs{Next: 1, Numbers: map[string]int{}}

	data, ok := config[secureBootIDsKey]
	if !ok {
		return ids, nil
	}

	err := json.Unmarshal([]byte(data), &ids)
	if err != nil {
		return sequentialIDs{}, fmt.Errorf("decode %s: %w", secureBootIDsKey, err)
	}

	if ids.Numbers == nil {
		ids.Numbers = map[string]int{}
	}

	return ids, nil
}

func (ids sequentialIDs) write(config map[string]string) error {
	data, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode %s: %w", secureBootIDsKey, err)
	}

	config[secureBootIDsKey] = string(data)

	return nil
}

// signatureRefs collects the entries with the Redfish IDs in the style of the profile.
func (s redfishServer) signatureRefs(db secureBootDatabase, lists []signatureList, certificates bool) ([]signatureRef, error) {
	refs := collectSignatures(lists, certificates)
	if s.profile.SecureBoot.CertificateIDStyle != profile.CertificateIDStyleSequential {
		return refs, nil
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return nil, err
	}

	ids, err := readSequentialIDs(instance.Config)
	if err != nil {
		return nil, err
	}

	if !numbered(ids, db, refs) {
		ids, err = s.numberSignatures()
		if err != nil {
			return nil, err
		}
	}

	for i, ref := range refs {
		refs[i].id = s.profile.SecureBoot.CertificateIDPrefix + strconv.Itoa(ids.Numbers[sequentialIDKey(db, ref.hash)])
	}

	return refs, nil
}

func numbered(ids sequentialIDs, db secureBootDatabase, refs []signatureRef) bool {
	for _, ref := range refs {
		_, ok := ids.Numbers[sequentialIDKey(db, ref.hash)]
		if !ok {
			return false
		}
	}

	return true
}

// numberSignatures gives a number to every entry without one, in the order of the databases.
// It drops the numbers of entries that are gone, for example because the guest changed the databases.
func (s redfishServer) numberSignatures() (sequentialIDs, error) {
	present := map[string]bool{}

	var order []string

	for _, db := range secureBootDatabases {
		lists, _, _, err := s.getSignatureDatabase(db)
		if err != nil {
			return sequentialIDs{}, err
		}

		for _, certificates := range []bool{true, false} {
			for _, ref := range collectSignatures(lists, certificates) {
				key := sequentialIDKey(db, ref.hash)
				present[key] = true
				order = append(order, key)
			}
		}
	}

	var ids sequentialIDs

	err := s.updateInstance(func(_ *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		var err error

		ids, err = readSequentialIDs(put.Config)
		if err != nil {
			return false, err
		}

		if put.Config == nil {
			put.Config = map[string]string{}
		}

		for key := range ids.Numbers {
			if !present[key] {
				delete(ids.Numbers, key)
			}
		}

		for _, key := range order {
			_, ok := ids.Numbers[key]
			if !ok {
				ids.Numbers[key] = ids.Next
				ids.Next++
			}
		}

		return true, ids.write(put.Config)
	})

	return ids, err
}

// forgetSignature drops the number of a deleted entry, so the entry gets a new number if a client adds it again.
func (s redfishServer) forgetSignature(db secureBootDatabase, hash string) error {
	if s.profile.SecureBoot.CertificateIDStyle != profile.CertificateIDStyleSequential {
		return nil
	}

	return s.updateInstance(func(_ *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		ids, err := readSequentialIDs(put.Config)
		if err != nil {
			return false, err
		}

		key := sequentialIDKey(db, hash)

		_, ok := ids.Numbers[key]
		if !ok {
			return false, nil
		}

		delete(ids.Numbers, key)

		return true, ids.write(put.Config)
	})
}

// resetSequentialIDs drops the numbers of the reset databases. Resetting all databases restarts the counter.
func (s redfishServer) resetSequentialIDs(databases []secureBootDatabase) error {
	if s.profile.SecureBoot.CertificateIDStyle != profile.CertificateIDStyleSequential {
		return nil
	}

	return s.updateInstance(func(_ *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		_, ok := put.Config[secureBootIDsKey]
		if !ok {
			return false, nil
		}

		if len(databases) == len(secureBootDatabases) {
			delete(put.Config, secureBootIDsKey)

			return true, nil
		}

		ids, err := readSequentialIDs(put.Config)
		if err != nil {
			return false, err
		}

		for key := range ids.Numbers {
			for _, db := range databases {
				if strings.HasPrefix(key, db.id+"/") {
					delete(ids.Numbers, key)
				}
			}
		}

		return true, ids.write(put.Config)
	})
}
