package api

import "testing"

func TestNodeEventPolicyHelpers(t *testing.T) {
	if !containsNodeEventName(NodeLiveOnlyEvents(), "node.traffic.report") {
		t.Fatalf("NodeLiveOnlyEvents() = %v, want node.traffic.report", NodeLiveOnlyEvents())
	}
	if !containsNodeEventName(NodeLiveOnlyEvents(), "webhook.delivery_succeeded") {
		t.Fatalf("NodeLiveOnlyEvents() = %v, want webhook.delivery_succeeded", NodeLiveOnlyEvents())
	}
	if !containsNodeEventName(NodeLiveOnlyEvents(), "callbacks_queue.updated") {
		t.Fatalf("NodeLiveOnlyEvents() = %v, want callbacks_queue.updated", NodeLiveOnlyEvents())
	}
	if !containsNodeEventName(NodeLiveOnlyEvents(), "management_platforms.updated") {
		t.Fatalf("NodeLiveOnlyEvents() = %v, want management_platforms.updated", NodeLiveOnlyEvents())
	}
	if !containsNodeEventName(NodeLiveOnlyEvents(), "operations.updated") {
		t.Fatalf("NodeLiveOnlyEvents() = %v, want operations.updated", NodeLiveOnlyEvents())
	}
	if !containsNodeEventName(NodeEphemeralEvents(), "client.traffic.reported") {
		t.Fatalf("NodeEphemeralEvents() = %v, want client.traffic.reported", NodeEphemeralEvents())
	}
	if ShouldPersistNodeEventName("node.traffic.report") {
		t.Fatal("ShouldPersistNodeEventName(node.traffic.report) = true, want false")
	}
	if ShouldPersistNodeEventName("client.traffic.reported") {
		t.Fatal("ShouldPersistNodeEventName(client.traffic.reported) = true, want false")
	}
	if !ShouldPersistNodeEventName("client.created") {
		t.Fatal("ShouldPersistNodeEventName(client.created) = false, want true")
	}
	if ShouldPersistNodeEventName("webhook.delivery_failed") {
		t.Fatal("ShouldPersistNodeEventName(webhook.delivery_failed) = true, want false")
	}
	if ShouldPersistNodeEventName("callbacks_queue.updated") {
		t.Fatal("ShouldPersistNodeEventName(callbacks_queue.updated) = true, want false")
	}
	if ShouldPersistNodeEventName("management_platforms.updated") {
		t.Fatal("ShouldPersistNodeEventName(management_platforms.updated) = true, want false")
	}
	if ShouldPersistNodeEventName("operations.updated") {
		t.Fatal("ShouldPersistNodeEventName(operations.updated) = true, want false")
	}
	if ShouldDeliverNodeCallbackEventName("node.traffic.report") {
		t.Fatal("ShouldDeliverNodeCallbackEventName(node.traffic.report) = true, want false")
	}
	if !ShouldDeliverNodeCallbackEventName("client.traffic.reported") {
		t.Fatal("ShouldDeliverNodeCallbackEventName(client.traffic.reported) = false, want true")
	}
	if ShouldDeliverNodeSinkEventName("webhook.delivery_succeeded") {
		t.Fatal("ShouldDeliverNodeSinkEventName(webhook.delivery_succeeded) = true, want false")
	}
	if ShouldDeliverNodeSinkEventName("callbacks_queue.updated") {
		t.Fatal("ShouldDeliverNodeSinkEventName(callbacks_queue.updated) = true, want false")
	}
	if ShouldDeliverNodeSinkEventName("management_platforms.updated") {
		t.Fatal("ShouldDeliverNodeSinkEventName(management_platforms.updated) = true, want false")
	}
	if ShouldDeliverNodeSinkEventName("operations.updated") {
		t.Fatal("ShouldDeliverNodeSinkEventName(operations.updated) = true, want false")
	}
}
