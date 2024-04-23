package contract

type Orgs struct {
	Name  string `json:"name"`
	Id    string `json:"id"`
	Group Group  `json:"group"`
}

type Group struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type UserMe struct {
	Id       *string `json:"id"`
	UserName *string `json:"username"`
	Email    *string `json:"email"`
	Orgs     []Orgs  `json:"orgs"`
}
