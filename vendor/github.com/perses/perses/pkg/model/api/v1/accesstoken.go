package v1

import (
	"encoding/json"
	"time"

	modelAPI "github.com/perses/perses/pkg/model/api"
)

type AccessToken struct {
	ID             int64  `json:"id"`
	UID            int64  `json:"uid"`
	Name           string `json:"name"`
	Token          string `json:"token"`
	TokenHash      string `json:"token_hash"`
	TokenSalt      string `json:"token_salt"`
	TokenLastEight string `json:"token_last_eight"`

	CreatedUnix time.Time `json:"created_at"`
	UpdatedUnix time.Time `json:"updated_at"`
	HasUsed     bool      `json:"has_used"`
	ExpDate     time.Time `json:"exp_date"`
}

func (a *AccessToken) SetUserType(userType string) {
	//TODO implement me
	panic("implement me")
}

func (a *AccessToken) GetMetadata() modelAPI.Metadata {
	return nil
}

func (a *AccessToken) SetUserID(id int64) {
	a.UID = id
}

func (a *AccessToken) SetProjectID(id int64) {
}

func (a *AccessToken) GetKind() string {
	return ""
}

func (a *AccessToken) GetSpec() any {
	return nil
}

func (a *AccessToken) SetFolderID(id int64) {
}

func (a *AccessToken) UnmarshalJSON(data []byte) error {
	var tmp AccessToken
	type plain AccessToken
	if err := json.Unmarshal(data, (*plain)(&tmp)); err != nil {
		return err
	}
	*a = tmp
	return nil
}

func (a *AccessToken) UnmarshalYAML(unmarshal func(any) error) error {
	var tmp AccessToken
	type plain AccessToken
	if err := unmarshal((*plain)(&tmp)); err != nil {
		return err
	}
	*a = tmp
	return nil
}
