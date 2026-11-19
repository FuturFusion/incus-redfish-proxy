package profile

import "fmt"

// Answers that several keys share.
const (
	// AnswerOK answers 200 with a top level @Message.ExtendedInfo.
	AnswerOK = "200"
	// AnswerOKError answers 200 with the messages inside an error object, like some BMCs do on success.
	AnswerOKError = "200-error"
)

// ResourcePatchResponse is the answer to a PATCH of the Systems, SecureBoot and VirtualMedia resources.
type ResourcePatchResponse string

const (
	// ResourcePatchResponseNoContent answers 204.
	ResourcePatchResponseNoContent ResourcePatchResponse = "204"
	// ResourcePatchResponseResource answers 200 with the patched resource.
	ResourcePatchResponseResource ResourcePatchResponse = "200-resource"
	// ResourcePatchResponseOK answers 200 with a top level @Message.ExtendedInfo.
	ResourcePatchResponseOK ResourcePatchResponse = AnswerOK
	// ResourcePatchResponseOKError answers 200 with the messages inside an error object.
	ResourcePatchResponseOKError ResourcePatchResponse = AnswerOKError
)

// Responses describes the answers that are not specific to one area.
type Responses struct {
	// Patch does not apply to the BIOS, which has bios.patch_response.
	Patch ResourcePatchResponse `yaml:"patch"`
}

func (r Responses) validate() error {
	switch r.Patch {
	case ResourcePatchResponseNoContent, ResourcePatchResponseResource, ResourcePatchResponseOK, ResourcePatchResponseOKError:

	default:
		return fmt.Errorf("unknown responses.patch %q", r.Patch)
	}

	return nil
}
