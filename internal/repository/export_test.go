package repository

// TestDB exposes the integration-test DB helper to the external repository_test package
// (which may import handlers without an import cycle).
var TestDB = testDB
