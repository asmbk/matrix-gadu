package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

const loginTimeout = 60 * time.Second

var loginFlows = []bridgev2.LoginFlow{{
	Name:        "Password",
	Description: "Log in with your GG number and password",
	ID:          "password",
}}

func (gc *GaduConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return loginFlows
}

func (gc *GaduConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != "password" {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &PasswordLogin{User: user, Main: gc}, nil
}

func (gc *GaduConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	uin, err := parseUserLoginID(login.ID)
	if err != nil {
		return err
	}
	login.Client = newClient(gc, login, uin)
	return nil
}

type PasswordLogin struct {
	User *bridgev2.User
	Main *GaduConnector
}

var _ bridgev2.LoginProcessUserInput = (*PasswordLogin)(nil)

func (p *PasswordLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeUserInput,
		StepID: "pl.gadu.password",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{
				Type:        bridgev2.LoginInputFieldTypeUsername,
				ID:          "uin",
				Name:        "GG number",
				Description: "Your Gadu-Gadu number",
				Pattern:     `^[0-9]+$`,
				Validate: func(s string) (string, error) {
					uin, err := gadu.ParseUIN(s)
					return uin.String(), err
				},
			}, {
				Type:        bridgev2.LoginInputFieldTypePassword,
				ID:          "password",
				Name:        "Password",
				Description: "Your Gadu-Gadu password",
			}},
		},
	}, nil
}

func (p *PasswordLogin) Cancel() {}

func (p *PasswordLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	uin, err := gadu.ParseUIN(input["uin"])
	if err != nil {
		return nil, err
	}
	password := input["password"]
	if password == "" {
		return nil, fmt.Errorf("the password is empty")
	}

	// Test the password with a short session before the login is saved.
	sess := p.Main.NewSession(p.Main.sessionConfig(ctx, uin, password, ""))
	connectCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	err = sess.Connect(connectCtx)
	cancel()
	_ = sess.Close()
	if err != nil {
		return nil, err
	}

	loginID := makeUserLoginID(uin)
	// NewLogin replaces the client of an existing login without stopping it.
	if existing, _ := p.Main.br.GetExistingUserLoginByID(ctx, loginID); existing != nil && existing.Client != nil {
		existing.Client.Disconnect()
	}
	ul, err := p.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: uin.String(),
		RemoteProfile: status.RemoteProfile{
			Username: uin.String(),
		},
		Metadata: &UserLoginMetadata{Password: password},
	}, &bridgev2.NewLoginParams{
		DeleteOnConflict: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save the login: %w", err)
	}
	ul.Client.Connect(ul.Log.WithContext(context.WithoutCancel(ctx)))
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "pl.gadu.complete",
		Instructions: fmt.Sprintf("Logged in as %s", uin),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}
