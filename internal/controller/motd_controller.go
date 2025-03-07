/*
Copyright 2025 TwistedSolutions.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	dispatchv1alpha1 "twistedsolutions.se/twisted-dispatch/api/v1alpha1"
)

type MessageLine struct {
	Kind    string // "text" or "divider"
	Content string // used only when Kind=="text"
}

// MotdReconciler reconciles a Motd object
type MotdReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dispatch.twistedsolutions.se,resources=motds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dispatch.twistedsolutions.se,resources=motds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dispatch.twistedsolutions.se,resources=motds/finalizers,verbs=update

// +kubebuilder:rbac:namespace=openshift,groups="",resources=configmaps,resourceNames=motd,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Motd object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *MotdReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var motd dispatchv1alpha1.Motd
	if err := r.Get(ctx, req.NamespacedName, &motd); err != nil {
		logger.Error(err, "unable to fetch Motd")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var messageLines []MessageLine
	for _, component := range motd.Spec.Components {
		switch component.Type {
		case "weather":
			var secret corev1.Secret
			secretKey := types.NamespacedName{
				Name:      component.WeatherAPIKeySecretRef.Name,
				Namespace: motd.Namespace,
			}
			if err := r.Get(ctx, secretKey, &secret); err != nil {
				logger.Error(err, "unable to fetch secret", "secret", component.WeatherAPIKeySecretRef.Name)
				continue
			}
			apiKeyBytes, exists := secret.Data[component.WeatherAPIKeySecretRef.Key]
			if !exists {
				logger.Error(fmt.Errorf("secret missing key"), "secret does not contain key", "secret", component.WeatherAPIKeySecretRef.Name, "key", component.WeatherAPIKeySecretRef.Key)
				continue
			}
			weatherData, err := fetchWeatherData(component.City, string(apiKeyBytes))
			if err != nil {
				logger.Error(err, "failed to fetch weather data")
				continue
			}
			// Convert weather data to a text line.
			messageLines = append(messageLines, MessageLine{
				Kind:    "text",
				Content: weatherData,
			})
		case "clusterOperatorStatus":
			clusterOperatorStatus, err := fetchClusterOperatorStatus(ctx, r.Client)
			if err != nil {
				logger.Error(err, "failed to fetch cluster operator status")
				continue
			}
			messageLines = append(messageLines, MessageLine{
				Kind:    "text",
				Content: clusterOperatorStatus,
			})
		case "nodeStatus":
			nodeStatus, err := fetchNodeStatus(ctx, r.Client)
			if err != nil {
				logger.Error(err, "failed to fetch node status")
				continue
			}
			messageLines = append(messageLines, MessageLine{
				Kind:    "text",
				Content: nodeStatus,
			})
		case "text":
			var stylePrefix string
			for _, s := range component.Style {
				switch s {
				case "underline":
					stylePrefix += "\033[4m"
				case "bold":
					stylePrefix += "\033[1m"
				case "italic":
					stylePrefix += "\033[3m"
				}
			}
			styledText := component.Text
			if stylePrefix != "" {
				styledText = stylePrefix + component.Text + "\033[0m"
			}
			messageLines = append(messageLines, MessageLine{
				Kind:    "text",
				Content: styledText,
			})
		case "divider":
			messageLines = append(messageLines, MessageLine{
				Kind: "divider",
			})
		default:
			logger.Info("unknown component type", "type", component.Type)
		}
	}
	finalMessage := "\n" + createFormattedMessage(messageLines)

	cmName := "motd"
	cmNamespace := "openshift"
	var cm corev1.ConfigMap
	cmKey := types.NamespacedName{
		Name:      cmName,
		Namespace: cmNamespace,
	}
	if err := r.Get(ctx, cmKey, &cm); err != nil {
		if errors.IsNotFound(err) {
			cm = corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: cmNamespace,
				},
				Data: map[string]string{
					"message": finalMessage,
				},
			}
			if err := r.Create(ctx, &cm); err != nil {
				// If a race condition caused the configmap to be created concurrently,
				// fetch it and update instead.
				if errors.IsAlreadyExists(err) {
					if err := r.Get(ctx, cmKey, &cm); err != nil {
						return ctrl.Result{}, err
					}
					cm.Data["motd"] = finalMessage
					if err := r.Update(ctx, &cm); err != nil {
						return ctrl.Result{}, err
					}
				} else {
					return ctrl.Result{}, err
				}
			}
		} else {
			return ctrl.Result{}, err
		}
	} else {
		cm.Data = map[string]string{
			"message": finalMessage,
		}
		if err := r.Update(ctx, &cm); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Requeue after a minute; adjust as needed per your refresh logic.
	return ctrl.Result{RequeueAfter: time.Duration(motd.Spec.RefreshInterval) * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MotdReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dispatchv1alpha1.Motd{}).
		Complete(r)
}

func weatherEmoji(condition string) string {
	switch condition {
	case "Sunny", "Clear":
		return "☀️"
	case "Partly Cloudy":
		return "⛅"
	case "Cloudy", "Overcast":
		return "☁️"
	case "Rain", "Drizzle":
		return "🌧️"
	case "Snow":
		return "❄️"
	case "Mist", "Fog":
		return "🌫️"
	default:
		return ""
	}
}

type WeatherForecastResponse struct {
	Forecast struct {
		Forecastday []struct {
			Date string `json:"date"`
			Day  struct {
				MintempC  float64 `json:"mintemp_c"`
				MaxtempC  float64 `json:"maxtemp_c"`
				Condition struct {
					Text string `json:"text"`
				} `json:"condition"`
			} `json:"day"`
		} `json:"forecastday"`
	} `json:"forecast"`
}

// fetchWeatherData calls weatherapi.com's forecast endpoint to retrieve the forecast for today.
// It extracts the minimum and maximum temperatures as well as the condition text.
func fetchWeatherData(city, apiKey string) (string, error) {
	// Build the forecast endpoint URL, limiting to 1 day (today).
	url := fmt.Sprintf("http://api.weatherapi.com/v1/forecast.json?key=%s&q=%s&days=1", apiKey, city)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to get weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("weatherapi.com returned status: %d", resp.StatusCode)
	}

	var wf WeatherForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&wf); err != nil {
		return "", fmt.Errorf("failed to decode weather response: %w", err)
	}

	if len(wf.Forecast.Forecastday) == 0 {
		return "", fmt.Errorf("no forecast data available")
	}

	day := wf.Forecast.Forecastday[0].Day
	originalCondition := strings.TrimSpace(day.Condition.Text)
	emoji := weatherEmoji(originalCondition)
	conditionText := originalCondition
	if emoji != "" {
		conditionText = fmt.Sprintf("%s %s", emoji, originalCondition)
	}

	return fmt.Sprintf("Forecast for %s: %s with temperatures from %.1f°C to %.1f°C", city, conditionText, day.MintempC, day.MaxtempC), nil
}

func fetchNodeStatus(ctx context.Context, c client.Client) (string, error) {
	var nodeList corev1.NodeList
	if err := c.List(ctx, &nodeList); err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}
	var unhealthyNodes []string
	var maintenanceNodes []string

	for _, node := range nodeList.Items {
		// All unschedulable nodes are considered under maintenance
		if node.Spec.Unschedulable {
			maintenanceNodes = append(maintenanceNodes, node.Name)
			continue
		}
		// For schedulable nodes, check the Ready condition.
		var readyCondition *corev1.NodeCondition
		for i, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				readyCondition = &node.Status.Conditions[i]
				break
			}
		}
		if readyCondition == nil || readyCondition.Status != corev1.ConditionTrue {
			unhealthyNodes = append(unhealthyNodes, node.Name)
		}
	}

	if len(unhealthyNodes) == 0 && len(maintenanceNodes) == 0 {
		return "✅ All Nodes Healthy", nil
	}

	var parts []string
	if len(unhealthyNodes) > 0 {
		parts = append(parts, fmt.Sprintf("❌ Unhealthy Node: %s", strings.Join(unhealthyNodes, ", ")))
	}
	if len(maintenanceNodes) > 0 {
		parts = append(parts, fmt.Sprintf("🚧 Maintenance on Node: %s", strings.Join(maintenanceNodes, ", ")))
	}

	return strings.Join(parts, "; "), nil
}

// fetchClusterOperatorStatus lists all cluster operators and checks their conditions.
// It returns a healthy message if all are OK; otherwise it lists those that are unhealthy.
func fetchClusterOperatorStatus(ctx context.Context, c client.Client) (string, error) {
	var coList configv1.ClusterOperatorList
	if err := c.List(ctx, &coList); err != nil {
		return "", fmt.Errorf("failed to list cluster operators: %w", err)
	}

	var unhealthyOperators []string
	for _, co := range coList.Items {
		available := false
		degraded := false
		for _, cond := range co.Status.Conditions {
			// Use OpenShift provided condition types.
			if cond.Type == configv1.OperatorAvailable {
				available = cond.Status == configv1.ConditionTrue
			}
			if cond.Type == configv1.OperatorDegraded {
				degraded = cond.Status == configv1.ConditionTrue
			}
		}

		// Consider the operator unhealthy if it's not available or is degraded.
		if !available || degraded {
			unhealthyOperators = append(unhealthyOperators, co.Name)
		}
	}

	if len(unhealthyOperators) == 0 {
		return "✅ All Cluster Operators Healthy", nil
	}

	return fmt.Sprintf("❌ Unhealthy Cluster Operators: %s", strings.Join(unhealthyOperators, ", ")), nil
}

// createFormattedMessage assembles a dynamically bordered, multiline message
// using the data provided by the components.
func createFormattedMessage(lines []MessageLine) string {
	indent := "  "
	maxWidth := 0
	// Determine the maximum width of the text (ignoring any accidental extra spaces).
	for _, line := range lines {
		if line.Kind == "text" {
			trimmed := strings.TrimSpace(line.Content)
			width := runewidth.StringWidth(trimmed)
			if width > maxWidth {
				maxWidth = width
			}
		}
	}
	if maxWidth == 0 {
		maxWidth = 20
	}
	// Total width = indent width + max text width.
	fullWidth := runewidth.StringWidth(indent) + maxWidth

	// Use heavy horizontal lines (━) for the top and bottom borders.
	topBorder := strings.Repeat("━", fullWidth)
	bottomBorder := topBorder

	// Use light horizontal lines (─) for dividers.
	dividerLine := strings.Repeat("─", fullWidth)

	var sb strings.Builder
	sb.WriteString(topBorder + "\n")
	for _, line := range lines {
		switch line.Kind {
		case "text":
			trimmed := strings.TrimSpace(line.Content)
			padded := runewidth.FillRight(trimmed, maxWidth)
			sb.WriteString(fmt.Sprintf("%s%s\n", indent, padded))
		case "divider":
			sb.WriteString(dividerLine + "\n")
		}
	}
	sb.WriteString(bottomBorder)
	return sb.String()
}
