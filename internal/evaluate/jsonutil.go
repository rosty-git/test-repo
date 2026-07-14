package evaluate

import "encoding/json"

// stringList unmarshals a JSON array of strings normally, but also
// tolerates a model returning a bare string (including "") for an
// empty-array field instead of []string{} - observed in practice from
// tool-use output despite the declared schema.
type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = arr
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	if str == "" {
		*s = nil
	} else {
		*s = []string{str}
	}
	return nil
}
