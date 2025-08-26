# HTTP API Tests

This directory contains HTTP test files for the Project Readiness API that can be used with the VSCode REST Client extension.

## Prerequisites

1. **Install VSCode REST Client Extension**:
   - Open VSCode
   - Go to Extensions (Ctrl+Shift+X)
   - Search for "REST Client" by Huachao Mao
   - Install the extension

2. **Start the API Server**:
   ```bash
   # From the project root
   make run
   # Or
   go run cmd/api/main.go
   ```

   The API will be available at `http://localhost:8080`

## Test Files

### 1. `projects.http`
Main API functionality tests covering all CRUD operations:
- Health check
- Create projects with different configurations
- List projects with pagination
- Get individual projects
- Update project checks
- Delete projects
- Error scenarios for duplicate creation, missing projects, etc.

### 2. `workflow.http`
Simulates a typical workflow of how the separate GitLab checking service would interact with this API:
1. Create project with initial failed checks
2. Gradually update checks as team fixes issues
3. Achieve production readiness
4. Handle regressions

### 3. `error-scenarios.http`
Tests error handling and edge cases:
- Invalid endpoints
- Malformed JSON
- Missing required fields
- Invalid parameters
- Special characters in project IDs

### 4. `performance.http`
Performance and load testing scenarios:
- Create multiple projects quickly
- Test pagination with different page sizes
- Rapid individual lookups
- Frequent updates simulation

## How to Use

1. **Open any `.http` file** in VSCode
2. **Click "Send Request"** above any HTTP request, or use `Ctrl+Alt+R`
3. **View responses** in the right panel
4. **Run requests sequentially** to build up test data

## Example Usage

1. Start with `projects.http` to test basic functionality
2. Use `workflow.http` to understand the typical usage pattern
3. Try `error-scenarios.http` to verify error handling
4. Use `performance.http` to test with multiple projects

## Variables

All test files use the variable:
```
@baseUrl = http://localhost:8080/api/v1
```

If your API runs on a different port, update this variable in each file.

## Expected Responses

### Successful Responses

**Health Check:**
```json
{
  "status": "success",
  "code": 200,
  "message": "Service is healthy",
  "timestamp": "2025-01-31T12:00:00Z",
  "data": {
    "status": "healthy",
    "service": "gitlab-readiness-api"
  }
}
```

**Project with Analysis:**
```json
{
  "status": "success",
  "code": 200,
  "message": "Project retrieved successfully",
  "timestamp": "2025-01-31T12:00:00Z",
  "data": {
    "project": {
      "project_id": "example-123",
      "project_present": true,
      "app_name_set": true,
      // ... other checks
    },
    "is_production_ready": false,
    "failed_checks": ["moab_id_set", "branch_protection_enabled"]
  }
}
```

**Project List:**
```json
{
  "status": "success",
  "code": 200,
  "message": "Projects retrieved successfully",
  "timestamp": "2025-01-31T12:00:00Z",
  "data": [
    {
      "project_id": "project-1",
      // ... project data
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 5
  }
}
```

### Error Responses

All errors follow this format:
```json
{
  "status": "error",
  "code": 404,
  "message": "project_id not found",
  "timestamp": "2025-01-31T12:00:00Z"
}
```

## Tips

1. **Sequential Execution**: Run create requests before get/update requests
2. **Clean Up**: Use delete requests to clean up test data
3. **Check Status Codes**: Look for 200/201 for success, 400/404/409/500 for errors
4. **Use Variables**: The @baseUrl variable makes it easy to change the API endpoint
5. **Response Time**: Check the response times in the bottom panel for performance testing

## Debugging

If requests fail:
1. Ensure the API server is running (`make run`)
2. Check the server logs for error details
3. Verify the request format matches the API documentation
4. Check that test data exists (run create requests first)