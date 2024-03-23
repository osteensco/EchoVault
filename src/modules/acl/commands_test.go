package acl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/echovault/echovault/src/server"
	"github.com/echovault/echovault/src/utils"
	"github.com/tidwall/resp"
	"net"
	"slices"
	"testing"
)

var bindAddr string
var port uint16
var mockServer *server.Server

var acl *ACL

func init() {
	bindAddr = "localhost"
	port = 7490

	mockServer = setUpServer(bindAddr, port, true)

	go func() {
		mockServer.Start(context.Background())
	}()
}

func setUpServer(bindAddr string, port uint16, requirePass bool) *server.Server {
	config := utils.Config{
		BindAddr:       bindAddr,
		Port:           port,
		DataDir:        "",
		EvictionPolicy: utils.NoEviction,
		RequirePass:    requirePass,
		Password:       "password1",
	}

	acl = NewACL(config)
	acl.Users = append(acl.Users, generateInitialTestUsers()...)

	return server.NewServer(server.Opts{
		Config:   config,
		ACL:      acl,
		Commands: Commands(),
	})
}

func generateInitialTestUsers() []*User {
	// User with both hash password and plaintext password
	withPasswordUser := CreateUser("with_password_user")
	h := sha256.New()
	h.Write([]byte("password3"))
	withPasswordUser.Passwords = []Password{
		{PasswordType: PasswordPlainText, PasswordValue: "password2"},
		{PasswordType: PasswordSHA256, PasswordValue: string(h.Sum(nil))},
	}

	// User with NoPassword option
	noPasswordUser := CreateUser("no_password_user")
	noPasswordUser.Passwords = []Password{
		{PasswordType: PasswordPlainText, PasswordValue: "password4"},
	}
	noPasswordUser.NoPassword = true

	// Disabled user
	disabledUser := CreateUser("disabled_user")
	disabledUser.Passwords = []Password{
		{PasswordType: PasswordPlainText, PasswordValue: "password5"},
	}
	disabledUser.Enabled = false

	return []*User{
		withPasswordUser,
		noPasswordUser,
		disabledUser,
	}
}

// compareSlices compare the elements in 2 slices, it checks if every element is s1 is contained in s2
// and vice versa. It essentially does a deep equality comparison.
// This is done manually rather than using slices.Equal because it would be ideal to throw an error
// specifying exactly which items are missing in either slice.
func compareSlices[T comparable](res, expected []T) error {
	if len(res) != len(expected) {
		return fmt.Errorf("expected slice of length %d, got slice of length %d", len(expected), len(res))
	}
	// Check whether all elements in res are contained in expected
	for _, r := range res {
		if !slices.Contains(expected, r) {
			return fmt.Errorf("got response item %+v, but it's not contained in expected slices", r)
		}
	}
	// Check whether all elements in expected are contained in res
	for _, e := range expected {
		if !slices.Contains(res, e) {
			return fmt.Errorf("expected element %+v, not found in res slice", e)
		}
	}
	return nil
}

// compareUsers compares 2 users and checks if all their fields are equal
func compareUsers(user1, user2 *User) error {
	// Compare flags
	if user1.Username != user2.Username {
		return fmt.Errorf("mismatched usernames \"%s\", and \"%s\"", user1.Username, user2.Username)
	}
	if user1.Enabled != user2.Enabled {
		return fmt.Errorf("mismatched enabled flag \"%+v\", and \"%+v\"", user1.Enabled, user2.Enabled)
	}
	if user1.NoPassword != user2.NoPassword {
		return fmt.Errorf("mismatched nopassword flag \"%+v\", and \"%+v\"", user1.NoPassword, user2.NoPassword)
	}
	if user1.NoKeys != user2.NoKeys {
		return fmt.Errorf("mismatched nokeys flag \"%+v\", and \"%+v\"", user1.NoKeys, user2.NoKeys)
	}

	// Compare passwords
	for _, password1 := range user1.Passwords {
		if !slices.ContainsFunc(user2.Passwords, func(password2 Password) bool {
			return password1.PasswordType == password2.PasswordType && password1.PasswordValue == password2.PasswordValue
		}) {
			return fmt.Errorf("found password %+v in user1 that was not found in user2", password1)
		}
	}
	for _, password2 := range user2.Passwords {
		if !slices.ContainsFunc(user1.Passwords, func(password1 Password) bool {
			return password1.PasswordType == password2.PasswordType && password1.PasswordValue == password2.PasswordValue
		}) {
			return fmt.Errorf("found password %+v in user2 that was not found in user1", password2)
		}
	}

	// Compare permissions
	permissions := [][][]string{
		{user1.IncludedCategories, user2.IncludedCategories},
		{user1.ExcludedCategories, user2.ExcludedCategories},
		{user1.IncludedCommands, user2.IncludedCommands},
		{user1.ExcludedCommands, user2.ExcludedCommands},
		{user1.IncludedReadKeys, user2.IncludedReadKeys},
		{user1.IncludedWriteKeys, user2.IncludedWriteKeys},
		{user1.IncludedPubSubChannels, user2.IncludedPubSubChannels},
		{user1.ExcludedPubSubChannels, user2.ExcludedPubSubChannels},
	}
	for _, p := range permissions {
		if err := compareSlices(p[0], p[1]); err != nil {
			return err
		}
	}

	return nil
}

func generateSHA256Password(plain string) string {
	h := sha256.New()
	h.Write([]byte(plain))
	return string(h.Sum(nil))
}

func Test_HandleAuth(t *testing.T) {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", bindAddr, port))
	if err != nil {
		t.Error(err)
	}
	defer func() {
		_ = conn.Close()
	}()
	r := resp.NewConn(conn)

	tests := []struct {
		cmd     []resp.Value
		wantRes string
		wantErr string
	}{
		{ // 1. Authenticate with default user without specifying username
			cmd:     []resp.Value{resp.StringValue("AUTH"), resp.StringValue("password1")},
			wantRes: "OK",
			wantErr: "",
		},
		{ // 2. Authenticate with plaintext password
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("with_password_user"),
				resp.StringValue("password2"),
			},
			wantRes: "OK",
			wantErr: "",
		},
		{ // 3. Authenticate with SHA256 password
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("with_password_user"),
				resp.StringValue("password3"),
			},
			wantRes: "OK",
			wantErr: "",
		},
		{ // 4. Authenticate with no password user
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("no_password_user"),
				resp.StringValue("password4"),
			},
			wantRes: "OK",
			wantErr: "",
		},
		{ // 5. Fail to authenticate with disabled user
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("disabled_user"),
				resp.StringValue("password5"),
			},
			wantRes: "",
			wantErr: "Error user disabled_user is disabled",
		},
		{ // 6. Fail to authenticate with non-existent user
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("non_existent_user"),
				resp.StringValue("password6"),
			},
			wantRes: "",
			wantErr: "Error no user with username non_existent_user",
		},
		{ // 7. Command too short
			cmd:     []resp.Value{resp.StringValue("AUTH")},
			wantRes: "",
			wantErr: fmt.Sprintf("Error %s", utils.WrongArgsResponse),
		},
		{ // 8. Command too long
			cmd: []resp.Value{
				resp.StringValue("AUTH"),
				resp.StringValue("user"),
				resp.StringValue("password1"),
				resp.StringValue("password2"),
			},
			wantRes: "",
			wantErr: fmt.Sprintf("Error %s", utils.WrongArgsResponse),
		},
	}

	for _, test := range tests {
		if err = r.WriteArray(test.cmd); err != nil {
			t.Error(err)
		}
		rv, _, err := r.ReadValue()
		if err != nil {
			t.Error(err)
		}
		if test.wantErr != "" {
			if rv.Error().Error() != test.wantErr {
				t.Errorf("expected error response \"%s\", got \"%s\"", test.wantErr, rv.Error().Error())
			}
			continue
		}
		if rv.String() != test.wantRes {
			t.Errorf("expected response \"%s\", got \"%s\"", test.wantRes, rv.String())
		}
	}
}

func Test_HandleCat(t *testing.T) {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", bindAddr, port))
	if err != nil {
		t.Error(err)
	}
	defer func() {
		_ = conn.Close()
	}()
	r := resp.NewConn(conn)

	// Authenticate connection
	if err = r.WriteArray([]resp.Value{resp.StringValue("AUTH"), resp.StringValue("password1")}); err != nil {
		t.Error(err)
	}
	rv, _, err := r.ReadValue()
	if err != nil {
		t.Error(err)
	}
	if rv.String() != "OK" {
		t.Error("could not authenticate user")
	}

	// Since only ACL commands are loaded in this test suite, this test will only test against the
	// list of categories and commands available in the ACL module.
	tests := []struct {
		cmd     []resp.Value
		wantRes []string
		wantErr string
	}{
		{ // 1. Return list of categories
			cmd: []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT")},
			wantRes: []string{
				utils.ConnectionCategory,
				utils.SlowCategory,
				utils.FastCategory,
				utils.AdminCategory,
				utils.DangerousCategory,
			},
			wantErr: "",
		},
		{ // 2. Return list of commands in connection category
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue(utils.ConnectionCategory)},
			wantRes: []string{"auth"},
			wantErr: "",
		},
		{ // 3. Return list of commands in slow category
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue(utils.SlowCategory)},
			wantRes: []string{"auth", "acl|cat", "acl|users", "acl|setuser", "acl|getuser", "acl|deluser", "acl|list", "acl|load", "acl|save"},
			wantErr: "",
		},
		{ // 4. Return list of commands in fast category
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue(utils.FastCategory)},
			wantRes: []string{"acl|whoami"},
			wantErr: "",
		},
		{ // 5. Return list of commands in admin category
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue(utils.AdminCategory)},
			wantRes: []string{"acl|users", "acl|setuser", "acl|getuser", "acl|deluser", "acl|list", "acl|load", "acl|save"},
			wantErr: "",
		},
		{ // 6. Return list of commands in dangerous category
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue(utils.DangerousCategory)},
			wantRes: []string{"acl|users", "acl|setuser", "acl|getuser", "acl|deluser", "acl|list", "acl|load", "acl|save"},
			wantErr: "",
		},
		{ // 7. Return error when category does not exist
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue("non-existent")},
			wantRes: nil,
			wantErr: "Error category NON-EXISTENT not found",
		},
		{ // 8. Command too long
			cmd:     []resp.Value{resp.StringValue("ACL"), resp.StringValue("CAT"), resp.StringValue("category1"), resp.StringValue("category2")},
			wantRes: nil,
			wantErr: fmt.Sprintf("Error %s", utils.WrongArgsResponse),
		},
	}

	for _, test := range tests {
		if err = r.WriteArray(test.cmd); err != nil {
			t.Error(err)
		}
		rv, _, err = r.ReadValue()
		if err != nil {
			t.Error(err)
		}
		if test.wantErr != "" {
			if rv.Error().Error() != test.wantErr {
				t.Errorf("expected error response \"%s\", got \"%s\"", test.wantErr, rv.Error().Error())
			}
			continue
		}
		resArr := rv.Array()
		// Check if all the elements in the expected array are in the response array
		for _, expected := range test.wantRes {
			if !slices.ContainsFunc(resArr, func(value resp.Value) bool {
				return value.String() == expected
			}) {
				t.Errorf("could not find expected command \"%s\" in the response array for category", expected)
			}
		}
		// Check if all the elements in the response array are in the expected array
		for _, value := range resArr {
			if !slices.ContainsFunc(test.wantRes, func(expected string) bool {
				return value.String() == expected
			}) {
				t.Errorf("could not find response command \"%s\" in the expected array", value.String())
			}
		}
	}
}

func Test_HandleUsers(t *testing.T) {
	var port uint16 = 7491
	mockServer := setUpServer(bindAddr, port, false)
	go func() {
		mockServer.Start(context.Background())
	}()

	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", bindAddr, port))
	if err != nil {
		t.Error(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	r := resp.NewConn(conn)

	users := []string{"default", "with_password_user", "no_password_user", "disabled_user"}

	if err = r.WriteArray([]resp.Value{resp.StringValue("ACL"), resp.StringValue("USERS")}); err != nil {
		t.Error(err)
	}

	rv, _, err := r.ReadValue()
	if err != nil {
		t.Error(err)
	}

	resArr := rv.Array()

	// Check if all the expected users are in the response array
	for _, user := range users {
		if !slices.ContainsFunc(resArr, func(value resp.Value) bool {
			return value.String() == user
		}) {
			t.Errorf("could not find expected user \"%s\" in response array", user)
		}
	}

	// Check if all the users in the response array are in the expected users
	for _, value := range resArr {
		if !slices.ContainsFunc(users, func(user string) bool {
			return value.String() == user
		}) {
			t.Errorf("could not find response user \"%s\" in expected users array", value.String())
		}
	}
}

func Test_HandleSetUser(t *testing.T) {
	var port uint16 = 7492
	mockServer := setUpServer(bindAddr, port, false)
	go func() {
		mockServer.Start(context.Background())
	}()
	acl, ok := mockServer.GetACL().(*ACL)
	if !ok {
		t.Error("error loading ACL module")
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", bindAddr, port))
	if err != nil {
		t.Error(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	r := resp.NewConn(conn)

	tests := []struct {
		presetUser *User
		cmd        []resp.Value
		wantRes    string
		wantErr    string
		wantUser   *User
	}{
		{
			// 1. Create new enabled user
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_1"),
				resp.StringValue("on"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_1")
				user.Enabled = true
				user.Normalise()
				return user
			}(),
		},
		{
			// 2. Create new disabled user
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_2"),
				resp.StringValue("off"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_2")
				user.Enabled = false
				user.Normalise()
				return user
			}(),
		},
		{
			// 3. Create new enabled user with both plaintext and SHA256 passwords
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_3"),
				resp.StringValue("on"),
				resp.StringValue(">set_user_3_plaintext_password_1"),
				resp.StringValue(">set_user_3_plaintext_password_2"),
				resp.StringValue(fmt.Sprintf("#%s", generateSHA256Password("set_user_3_hash_password_1"))),
				resp.StringValue(fmt.Sprintf("#%s", generateSHA256Password("set_user_3_hash_password_2"))),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_3")
				user.Enabled = true
				user.Passwords = []Password{
					{PasswordType: PasswordPlainText, PasswordValue: "set_user_3_plaintext_password_1"},
					{PasswordType: PasswordPlainText, PasswordValue: "set_user_3_plaintext_password_2"},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("set_user_3_hash_password_1")},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("set_user_3_hash_password_2")},
				}
				user.Normalise()
				return user
			}(),
		},
		{
			// 4. Remove plaintext and SHA256 password from existing user
			presetUser: func() *User {
				user := CreateUser("set_user_4")
				user.Enabled = true
				user.Passwords = []Password{
					{PasswordType: PasswordPlainText, PasswordValue: "set_user_3_plaintext_password_1"},
					{PasswordType: PasswordPlainText, PasswordValue: "set_user_3_plaintext_password_2"},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("set_user_3_hash_password_1")},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("set_user_3_hash_password_2")},
				}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_4"),
				resp.StringValue("on"),
				resp.StringValue("<set_user_3_plaintext_password_2"),
				resp.StringValue(fmt.Sprintf("!%s", generateSHA256Password("set_user_3_hash_password_2"))),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_4")
				user.Enabled = true
				user.Passwords = []Password{
					{PasswordType: PasswordPlainText, PasswordValue: "set_user_3_plaintext_password_1"},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("set_user_3_hash_password_1")},
				}
				user.Normalise()
				return user
			}(),
		},
		{
			// 5. Create user with no commands allowed to be executed
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_5"),
				resp.StringValue("on"),
				resp.StringValue("nocommands"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_5")
				user.Enabled = true
				user.ExcludedCommands = []string{"*"}
				user.ExcludedCategories = []string{"*"}
				user.Normalise()
				return user
			}(),
		},
		{
			// 6. Create user that can access all categories with +@*
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_6"),
				resp.StringValue("on"),
				resp.StringValue("+@*"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_6")
				user.Enabled = true
				user.IncludedCategories = []string{"*"}
				user.ExcludedCategories = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 7. Create user that can access all categories with allcategories flag
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_7"),
				resp.StringValue("on"),
				resp.StringValue("allcategories"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_7")
				user.Enabled = true
				user.IncludedCategories = []string{"*"}
				user.ExcludedCategories = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 8. Create user with a few allowed categories and a few disallowed categories
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_8"),
				resp.StringValue("on"),
				resp.StringValue(fmt.Sprintf("+@%s", utils.WriteCategory)),
				resp.StringValue(fmt.Sprintf("+@%s", utils.ReadCategory)),
				resp.StringValue(fmt.Sprintf("+@%s", utils.PubSubCategory)),
				resp.StringValue(fmt.Sprintf("-@%s", utils.AdminCategory)),
				resp.StringValue(fmt.Sprintf("-@%s", utils.ConnectionCategory)),
				resp.StringValue(fmt.Sprintf("-@%s", utils.DangerousCategory)),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_8")
				user.Enabled = true
				user.IncludedCategories = []string{utils.WriteCategory, utils.ReadCategory, utils.PubSubCategory}
				user.ExcludedCategories = []string{utils.AdminCategory, utils.ConnectionCategory, utils.DangerousCategory}
				user.Normalise()
				return user
			}(),
		},
		{
			// 9. Create user that is not allowed to access any keys
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_9"),
				resp.StringValue("on"),
				resp.StringValue("resetkeys"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_9")
				user.Enabled = true
				user.NoKeys = true
				user.IncludedReadKeys = []string{}
				user.IncludedWriteKeys = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 10. Create user that can access some read keys and some write keys
			// Provide keys that are RW, W-Only and R-Only
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_10"),
				resp.StringValue("on"),
				resp.StringValue("~key1"),
				resp.StringValue("~key2"),
				resp.StringValue("%RW~key3"),
				resp.StringValue("%RW~key4"),
				resp.StringValue("%R~key5"),
				resp.StringValue("%R~key6"),
				resp.StringValue("%W~key7"),
				resp.StringValue("%W~key8"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_10")
				user.Enabled = true
				user.NoKeys = false
				user.IncludedReadKeys = []string{"key1", "key2", "key3", "key4", "key5", "key6"}
				user.IncludedWriteKeys = []string{"key1", "key2", "key3", "key4", "key7", "key8"}
				user.Normalise()
				return user
			}(),
		},
		{
			// 11. Create user that can access all pubsub channels with +&*
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_11"),
				resp.StringValue("on"),
				resp.StringValue("+&*"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_11")
				user.Enabled = true
				user.IncludedPubSubChannels = []string{"*"}
				user.ExcludedPubSubChannels = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 12. Create user that can access all pubsub channels with allchannels flag
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_12"),
				resp.StringValue("on"),
				resp.StringValue("allchannels"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_12")
				user.Enabled = true
				user.IncludedPubSubChannels = []string{"*"}
				user.ExcludedPubSubChannels = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 13. Create user with a few allowed pubsub channels and a few disallowed channels
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_13"),
				resp.StringValue("on"),
				resp.StringValue("+&channel1"),
				resp.StringValue("+&channel2"),
				resp.StringValue("-&channel3"),
				resp.StringValue("-&channel4"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_13")
				user.Enabled = true
				user.IncludedPubSubChannels = []string{"channel1", "channel2"}
				user.ExcludedPubSubChannels = []string{"channel3", "channel4"}
				user.Normalise()
				return user
			}(),
		},
		{
			// 14. Create user that can access all commands
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_14"),
				resp.StringValue("on"),
				resp.StringValue("allcommands"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_14")
				user.Enabled = true
				user.IncludedCommands = []string{"*"}
				user.ExcludedCommands = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 15. Create user with some allowed commands and disallowed commands
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_15"),
				resp.StringValue("on"),
				resp.StringValue("+acl|getuser"),
				resp.StringValue("+acl|setuser"),
				resp.StringValue("+acl|deluser"),
				resp.StringValue("-rewriteaof"),
				resp.StringValue("-save"),
				resp.StringValue("-publish"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_15")
				user.Enabled = true
				user.IncludedCommands = []string{"acl|getuser", "acl|setuser", "acl|deluser"}
				user.ExcludedCommands = []string{"rewriteaof", "save", "publish"}
				user.Normalise()
				return user
			}(),
		},
		{
			// 16. Create new user with no password using 'nopass'.
			// When nopass is provided, ignore any passwords that may have been provided in the command.
			presetUser: nil,
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_16"),
				resp.StringValue("on"),
				resp.StringValue("nopass"),
				resp.StringValue(">password1"),
				resp.StringValue(fmt.Sprintf("#%s", generateSHA256Password("password2"))),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_16")
				user.Enabled = true
				user.NoPassword = true
				user.Passwords = []Password{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 17. Delete all existing users passwords using 'nopass'
			presetUser: func() *User {
				user := CreateUser("set_user_17")
				user.Enabled = true
				user.NoPassword = true
				user.Passwords = []Password{
					{PasswordType: PasswordPlainText, PasswordValue: "password1"},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("password2")},
				}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_17"),
				resp.StringValue("on"),
				resp.StringValue("nopass"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_17")
				user.Enabled = true
				user.NoPassword = true
				user.Passwords = []Password{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 18. Clear all of an existing user's passwords using 'resetpass'
			presetUser: func() *User {
				user := CreateUser("set_user_18")
				user.Enabled = true
				user.NoPassword = true
				user.Passwords = []Password{
					{PasswordType: PasswordPlainText, PasswordValue: "password1"},
					{PasswordType: PasswordSHA256, PasswordValue: generateSHA256Password("password2")},
				}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_18"),
				resp.StringValue("on"),
				resp.StringValue("nopass"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_18")
				user.Enabled = true
				user.NoPassword = true
				user.Passwords = []Password{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 19. Clear all of an existing user's command privileges using 'nocommands'
			presetUser: func() *User {
				user := CreateUser("set_user_19")
				user.Enabled = true
				user.IncludedCommands = []string{"acl|getuser", "acl|setuser", "acl|deluser"}
				user.ExcludedCommands = []string{"rewriteaof", "save"}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_19"),
				resp.StringValue("on"),
				resp.StringValue("nocommands"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_19")
				user.Enabled = true
				user.IncludedCommands = []string{}
				user.ExcludedCommands = []string{"*"}
				user.IncludedCategories = []string{}
				user.ExcludedCategories = []string{"*"}
				user.Normalise()
				return user
			}(),
		},
		{
			// 20. Clear all of an existing user's allowed keys using 'resetkeys'
			presetUser: func() *User {
				user := CreateUser("set_user_20")
				user.Enabled = true
				user.IncludedWriteKeys = []string{"key1", "key2", "key3", "key4", "key5", "key6"}
				user.IncludedReadKeys = []string{"key1", "key2", "key3", "key7", "key8", "key9"}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_20"),
				resp.StringValue("on"),
				resp.StringValue("resetkeys"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_20")
				user.Enabled = true
				user.NoKeys = true
				user.IncludedReadKeys = []string{}
				user.IncludedWriteKeys = []string{}
				user.Normalise()
				return user
			}(),
		},
		{
			// 21. Allow user to access all channels using 'resetchannels'
			presetUser: func() *User {
				user := CreateUser("set_user_21")
				user.IncludedPubSubChannels = []string{"channel1", "channel2"}
				user.ExcludedPubSubChannels = []string{"channel3", "channel4"}
				user.Normalise()
				return user
			}(),
			cmd: []resp.Value{
				resp.StringValue("ACL"),
				resp.StringValue("SETUSER"),
				resp.StringValue("set_user_21"),
				resp.StringValue("resetchannels"),
			},
			wantRes: "OK",
			wantErr: "",
			wantUser: func() *User {
				user := CreateUser("set_user_21")
				user.IncludedPubSubChannels = []string{}
				user.ExcludedPubSubChannels = []string{"*"}
				user.Normalise()
				return user
			}(),
		},
	}

	for i, test := range tests {
		if test.presetUser != nil {
			acl.Users = append(acl.Users, test.presetUser)
		}
		if err = r.WriteArray(test.cmd); err != nil {
			t.Error(err)
		}
		v, _, err := r.ReadValue()
		if err != nil {
			t.Error(err)
		}
		if test.wantErr != "" {
			if v.Error().Error() != test.wantErr {
				t.Errorf("expected error response \"%s\", got \"%s\"", test.wantErr, v.Error().Error())
			}
			continue
		}
		if v.String() != test.wantRes {
			t.Errorf("expected response \"%s\", got \"%s\"", test.wantRes, v.String())
		}
		if test.wantUser == nil {
			continue
		}
		expectedUser := test.wantUser
		currUserIdx := slices.IndexFunc(acl.Users, func(user *User) bool {
			return user.Username == expectedUser.Username
		})
		if currUserIdx == -1 {
			t.Errorf("expected to find user with username \"%s\" but could not find them.", expectedUser.Username)
		}
		if err = compareUsers(expectedUser, acl.Users[currUserIdx]); err != nil {
			t.Errorf("test idx: %d, %+v", i, err)
		}
	}
}

func Test_HandleGetUser(t *testing.T) {}

func Test_HandleDelUser(t *testing.T) {}

func Test_HandleWhoAmI(t *testing.T) {}

func Test_HandleList(t *testing.T) {}

func Test_HandleLoad(t *testing.T) {}

func Test_HandleSave(t *testing.T) {}
