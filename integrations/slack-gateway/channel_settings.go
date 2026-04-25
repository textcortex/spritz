package main

import (
	"sort"
	"strings"
)

func (g *slackGateway) relativeGatewayPath(requestPath string) string {
	requestPath = strings.TrimSpace(requestPath)
	prefix := g.publicPathPrefix()
	if prefix != "" {
		if requestPath == prefix {
			return "/"
		}
		if strings.HasPrefix(requestPath, prefix+"/") {
			return strings.TrimPrefix(requestPath, prefix)
		}
	}
	return requestPath
}

func primaryManagedConnection(installation backendManagedInstallation) backendManagedConnection {
	for _, connection := range installation.Connections {
		if connection.IsDefault {
			return connection
		}
	}
	if len(installation.Connections) > 0 {
		return installation.Connections[0]
	}
	return backendManagedConnection{
		ID:        "",
		IsDefault: true,
		State:     installation.State,
		Routes:    routesFromInstallationConfig(installation.InstallationConfig),
	}
}

func managedConnectionByID(installation backendManagedInstallation, connectionID string) (backendManagedConnection, bool) {
	connectionID = strings.TrimSpace(connectionID)
	for _, connection := range installation.Connections {
		if strings.TrimSpace(connection.ID) == connectionID {
			return connection, true
		}
	}
	return backendManagedConnection{}, false
}

func routesFromInstallationConfig(config installationConfig) []backendManagedChannelRoute {
	routes := make([]backendManagedChannelRoute, 0, len(config.ChannelPolicies))
	for _, policy := range config.ChannelPolicies {
		requireMention := true
		if policy.RequireMention != nil {
			requireMention = *policy.RequireMention
		}
		enabled := true
		routes = append(routes, backendManagedChannelRoute{
			ExternalChannelID:   strings.TrimSpace(policy.ExternalChannelID),
			ExternalChannelType: strings.TrimSpace(policy.ExternalChannelType),
			RequireMention:      &requireMention,
			Enabled:             &enabled,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ExternalChannelID < routes[j].ExternalChannelID
	})
	return routes
}

func managedRouteEnabled(route backendManagedChannelRoute) bool {
	return route.Enabled == nil || *route.Enabled
}

func managedRouteRequireMention(route backendManagedChannelRoute) bool {
	return route.RequireMention == nil || *route.RequireMention
}

func channelPoliciesFromConnection(connection backendManagedConnection) []installationChannelPolicy {
	policies := make([]installationChannelPolicy, 0, len(connection.Routes))
	for _, route := range connection.Routes {
		if !managedRouteEnabled(route) {
			continue
		}
		channelID := strings.TrimSpace(route.ExternalChannelID)
		if channelID == "" {
			continue
		}
		requireMention := managedRouteRequireMention(route)
		policies = append(policies, installationChannelPolicy{
			ExternalChannelID:   channelID,
			ExternalChannelType: strings.TrimSpace(route.ExternalChannelType),
			RequireMention:      &requireMention,
		})
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].ExternalChannelID < policies[j].ExternalChannelID
	})
	return policies
}
