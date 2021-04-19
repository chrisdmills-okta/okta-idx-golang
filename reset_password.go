package idx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ResetPasswordResponse struct {
	idxContext     *Context
	token          *Token
	availableSteps []ResetPasswordStep
	sq             *SecurityQuestion
}

type IdentifyRequest struct {
	Identifier string `json:"identifier"`
	RememberMe bool   `json:"remember_me"`
}

func (c *Client) InitPasswordReset(ctx context.Context, ir *IdentifyRequest) (*ResetPasswordResponse, error) {
	idxContext, err := c.Interact(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.Introspect(context.TODO(), idxContext)
	if err != nil {
		return nil, err
	}
	ro, err := resp.remediationOption("identify")
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(ir)
	resp, err = ro.Proceed(ctx, b)
	if err != nil {
		return nil, err
	}
	if resp.CurrentAuthenticatorEnrollment == nil {
		if resp.Messages != nil {
			return nil, fmt.Errorf("falied to init password recovery: 'currentAuthenticatorEnrollment' field is missing from the response: %v", resp.Messages.Values)
		}
		return nil, fmt.Errorf("falied to init password recovery: 'currentAuthenticatorEnrollment' field is missing from the response")
	}
	resp, err = resp.CurrentAuthenticatorEnrollment.Value.Recover.Proceed(ctx, nil)
	if err != nil {
		return nil, err
	}
	rpr := &ResetPasswordResponse{
		idxContext: idxContext,
	}
	err = rpr.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	return rpr, nil
}

func (r *ResetPasswordResponse) VerifyEmail(ctx context.Context) (*ResetPasswordResponse, error) {
	if !r.HasStep(ResetPasswordStepEmailVerification) {
		return nil, fmt.Errorf("this step is not available, please try one of %s", r.AvailableSteps())
	}
	resp, err := idx.Introspect(ctx, r.idxContext)
	if err != nil {
		return nil, err
	}
	if resp.CurrentAuthenticatorEnrollment == nil {
		if resp.Messages != nil {
			return nil, fmt.Errorf("falied to init password recovery: 'currentAuthenticatorEnrollment' field is missing from the response: %v", resp.Messages.Values)
		}
		return nil, fmt.Errorf("falied to init password recovery: 'currentAuthenticatorEnrollment' field is missing from the response")
	}
	resp, err = resp.CurrentAuthenticatorEnrollment.Value.Recover.Proceed(ctx, nil)
	if err != nil {
		return nil, err
	}
	ro, authID, err := resp.authenticatorOption("select-authenticator-authenticate", "Email")
	if err != nil {
		return nil, err
	}
	authenticator := []byte(`{
				"authenticator": {
					"id": "` + authID + `"
				}
			}`)
	resp, err = ro.Proceed(ctx, authenticator)
	if err != nil {
		return nil, err
	}
	err = r.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	r.availableSteps = append(r.availableSteps, ResetPasswordStepEmailConfirmation)
	return r, nil
}

func (r *ResetPasswordResponse) ConfirmEmail(ctx context.Context, code string) (*ResetPasswordResponse, error) {
	if !r.HasStep(ResetPasswordStepEmailConfirmation) {
		return nil, fmt.Errorf("this step is not available, please try one of %s", r.AvailableSteps())
	}
	return r.confirmWithCode(ctx, code)
}

func (r *ResetPasswordResponse) AnswerSecurityQuestion(ctx context.Context, answer string) (*ResetPasswordResponse, error) {
	if !r.HasStep(ResetPasswordStepAnswerSecurityQuestion) {
		return nil, fmt.Errorf("this step is not available, please try one of %s", r.AvailableSteps())
	}
	resp, err := idx.Introspect(ctx, r.idxContext)
	if err != nil {
		return nil, err
	}
	ro, err := resp.remediationOption("challenge-authenticator")
	if err != nil {
		return nil, err
	}
	credentials := []byte(fmt.Sprintf(`{
				"credentials": {
					"questionKey": "%s",
					"answer": "%s"
				}
			}`, r.sq.QuestionKey, answer))
	resp, err = ro.Proceed(ctx, credentials)
	if err != nil {
		return nil, err
	}
	defer func() { r.sq = nil }() // remove security question to avid confusion
	err = r.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ResetPasswordResponse) SetNewPassword(ctx context.Context, password string) (*ResetPasswordResponse, error) {
	if !r.HasStep(ResetPasswordStepNewPassword) {
		return nil, fmt.Errorf("this step is not available, please try one of %s", r.AvailableSteps())
	}
	resp, err := idx.Introspect(ctx, r.idxContext)
	if err != nil {
		return nil, err
	}
	ro, err := resp.remediationOption("reset-authenticator")
	if err != nil {
		return nil, err
	}
	credentials := []byte(`{
		"credentials": {
			"passcode": "` + strings.TrimSpace(password) + `"
		}
	}`)
	resp, err = ro.Proceed(ctx, credentials)
	if err != nil {
		return nil, err
	}
	err = r.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Cancel the whole reset password process.
func (r *ResetPasswordResponse) Cancel(ctx context.Context) (*ResetPasswordResponse, error) {
	if !r.HasStep(ResetPasswordStepCancel) {
		return nil, fmt.Errorf("this step is not available, please try one of %s", r.AvailableSteps())
	}
	resp, err := idx.Introspect(ctx, r.idxContext)
	if err != nil {
		return nil, err
	}
	resp, err = resp.Cancel(ctx)
	if err != nil {
		return nil, err
	}
	err = r.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// SecurityQuestion should return SecurityQuestion object in case there is step 'ANSWER SECURITY_QUESTION'
// present in the available steps. It will have non-empty 'questionKey' (unique identifier)
// and 'question' (human readable question) fields
// In case 'ANSWER SECURITY_QUESTION' is not in the list of available steps, response will be nil.
func (r *ResetPasswordResponse) SecurityQuestion() *SecurityQuestion {
	return r.sq
}

// AvailableSteps returns list of steps that can be executed next.
// In case of successful authentication, list will contain only one "SUCCESS" step.
func (r *ResetPasswordResponse) AvailableSteps() []ResetPasswordStep {
	return r.availableSteps
}

// HasStep checks if the provided step is present in the list of available steps.
func (r *ResetPasswordResponse) HasStep(s ResetPasswordStep) bool {
	for i := range r.availableSteps {
		if r.availableSteps[i] == s {
			return true
		}
	}
	return false
}

// IsAuthenticated returns true in case "SUCCESS"is present in the list of available steps.
func (r *ResetPasswordResponse) IsAuthenticated() bool {
	return r.HasStep(ResetPasswordStepSuccess)
}

// Token returns authorization token. This method should be called when there is "SUCCESS" step
// present in the list of available steps.
func (r *ResetPasswordResponse) Token() *Token {
	return r.token
}

type ResetPasswordStep int

func (s ResetPasswordStep) String() string {
	v, ok := resetStepText[s]
	if ok {
		return v
	}
	return "UNKNOWN"
}

var resetStepText = map[ResetPasswordStep]string{
	ResetPasswordStepEmailVerification:      "EMAIL_VERIFICATION",
	ResetPasswordStepEmailConfirmation:      "EMAIL_CONFIRMATION",
	ResetPasswordStepAnswerSecurityQuestion: "ANSWER SECURITY_QUESTION",
	ResetPasswordStepNewPassword:            "NEW_PASSWORD",
	ResetPasswordStepCancel:                 "CANCEL",
	ResetPasswordStepSkip:                   "SKIP",
	ResetPasswordStepSuccess:                "SUCCESS",
}

// These codes indicate what method(s) can be called in the next step.
const (
	ResetPasswordStepEmailVerification      ResetPasswordStep = iota + 1 // 'VerifyEmail'
	ResetPasswordStepEmailConfirmation                                   // 'ConfirmEmail'
	ResetPasswordStepAnswerSecurityQuestion                              // 'AnswerSecurityQuestion'
	ResetPasswordStepNewPassword                                         // 'SetNewPassword'
	ResetPasswordStepCancel                                              // 'Cancel'
	ResetPasswordStepSkip                                                // 'Skip'
	ResetPasswordStepSuccess                                             // 'Token'
)

func (r *ResetPasswordResponse) setupNextSteps(ctx context.Context, resp *Response) error {
	if resp.LoginSuccess() {
		exchangeForm := []byte(`{
			"client_secret": "` + idx.ClientSecret() + `",
			"code_verifier": "` + r.idxContext.CodeVerifier() + `"
		}`)
		tokens, err := resp.SuccessResponse.ExchangeCode(ctx, exchangeForm)
		if err != nil {
			return err
		}
		r.token = tokens
		r.availableSteps = []ResetPasswordStep{ResetPasswordStepSuccess}
		return nil
	}
	var steps []ResetPasswordStep
	if resp.CancelResponse != nil {
		steps = append(steps, ResetPasswordStepCancel)
	}
	_, _, err := resp.authenticatorOption("select-authenticator-authenticate", "Email")
	if err == nil {
		steps = append(steps, ResetPasswordStepEmailVerification)
	}
	_, err = resp.remediationOption("skip")
	if err == nil {
		steps = append(steps, ResetPasswordStepSkip)
	}
	ro, err := resp.remediationOption("challenge-authenticator")
	if err == nil {
	loop:
		for i := range ro.FormValues {
			if ro.FormValues[i].Form != nil && len(ro.FormValues[i].Form.FormValues) > 0 {
				for j := range ro.FormValues[i].Form.FormValues {
					if ro.FormValues[i].Form.FormValues[j].Name == "questionKey" {
						r.sq = &SecurityQuestion{
							QuestionKey: ro.FormValues[i].Form.FormValues[j].Value,
							Question:    ro.FormValues[i].Form.FormValues[j].Label,
						}
						steps = append(steps, ResetPasswordStepAnswerSecurityQuestion)
						break loop
					}
				}
			}
		}
	}
	ro, err = resp.remediationOption("reset-authenticator")
	if err == nil {
	loop2:
		for i := range ro.FormValues {
			if ro.FormValues[i].Form != nil && len(ro.FormValues[i].Form.FormValues) > 0 {
				for j := range ro.FormValues[i].Form.FormValues {
					if ro.FormValues[i].Form.FormValues[j].Label == "New password" {
						steps = append(steps, ResetPasswordStepNewPassword)
						break loop2
					}
				}
			}
		}
	}
	if len(steps) == 0 {
		return fmt.Errorf("there are no more steps available: %v", resp.Messages.Values)
	}
	r.availableSteps = steps
	return nil
}

func (r *ResetPasswordResponse) confirmWithCode(ctx context.Context, code string) (*ResetPasswordResponse, error) {
	resp, err := idx.Introspect(ctx, r.idxContext)
	if err != nil {
		return nil, err
	}
	ro, err := resp.remediationOption("challenge-authenticator")
	if err != nil {
		return nil, err
	}
	credentials := []byte(fmt.Sprintf(`{
				"credentials": {
					"passcode": "%s"
				}
			}`, strings.TrimSpace(code)))
	resp, err = ro.Proceed(ctx, credentials)
	if err != nil {
		return nil, err
	}
	err = r.setupNextSteps(ctx, resp)
	if err != nil {
		return nil, err
	}
	return r, nil
}
