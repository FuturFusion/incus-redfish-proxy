package api_test

import (
	"errors"
	"net/http"
	"path"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func TestRedfishServer_TaskResource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		taskResource bool

		wantTasks int
	}{
		{name: "success - task service hidden", taskResource: false, wantTasks: http.StatusNotFound},
		{name: "success - task service exposed", taskResource: true, wantTasks: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.BIOS.TaskResource = tc.taskResource

			client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

			_, location := patchBIOS(t, client)
			taskID := path.Base(location)

			// Run test
			resp, err := client.Get("/redfish/v1/TaskService/Tasks/" + taskID)
			taskStatus := responseStatus(t, resp, err)

			resp, err = client.Get("/redfish/v1/TaskService/Tasks")
			tasksStatus := responseStatus(t, resp, err)

			// Assert
			require.Equal(t, http.StatusOK, taskStatus, "the @odata.id of every task body resolves")
			require.Equal(t, tc.wantTasks, tasksStatus, "the task collection follows bios.task_resource")
		})
	}
}

// responseStatus returns the status code of a response. The gofish client turns error status codes into errors.
func responseStatus(t *testing.T, resp *http.Response, err error) int {
	t.Helper()

	redfishErr := &schemas.Error{}
	if errors.As(err, &redfishErr) {
		return redfishErr.HTTPReturnedStatusCode
	}

	require.NoError(t, err, "the request succeeds")
	resp.Body.Close()

	return resp.StatusCode
}
