// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// modelGatewayClient keeps the desktop binding independent from the local
// gateway transport. Tests can provide a small fake without starting Wails or
// an HTTP listener.
type modelGatewayClient interface {
	States() (map[string]gatewayModelState, error)
	Action(modelID, action string) error
	Refresh() error
	Plan(modelID string) (*ModelPlacementPlan, error)
}

type localModelGatewayClient struct {
	baseURL string
	client  *http.Client
}

func newLocalModelGatewayClient() *localModelGatewayClient {
	return &localModelGatewayClient{baseURL: modelGatewayURL, client: &http.Client{Timeout: 15 * time.Second}}
}

func (c *localModelGatewayClient) States() (map[string]gatewayModelState, error) {
	response, err := c.client.Get(c.baseURL + "/api/v1/model-states")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model-state endpoint returned %d", response.StatusCode)
	}
	var payload struct {
		Models []gatewayModelState `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	states := make(map[string]gatewayModelState, len(payload.Models))
	for _, state := range payload.Models {
		states[state.Model] = state
	}
	return states, nil
}

func (c *localModelGatewayClient) Action(modelID, action string) error {
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/models/"+url.PathEscape(modelID)+"/"+action, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 10 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("contacting the local model gateway: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		return fmt.Errorf("model %s failed: %s", action, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *localModelGatewayClient) Refresh() error {
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/models/refresh", nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("refreshing the model library returned %d", response.StatusCode)
	}
	return nil
}

func (c *localModelGatewayClient) Plan(modelID string) (*ModelPlacementPlan, error) {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v1/models/"+url.PathEscape(modelID)+"/plan", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("getting model placement preview: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		return nil, fmt.Errorf("model placement preview failed: %s", strings.TrimSpace(string(body)))
	}
	var plan ModelPlacementPlan
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		return nil, fmt.Errorf("reading model placement preview: %w", err)
	}
	return &plan, nil
}
