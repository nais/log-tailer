# Code Style & Formatting:
Always follow the standard Go style guide and best practices as outlined in Effective Go and Go Code Review Comments.
Ensure all generated code is formatted using gofumpt standards.
Use meaningful variable and function names. Avoid overly short names unless they are standard (e.g., i for loops, err for errors).
Organize imports into standard and third-party groups, separated by a blank line.

# Error Handling:
Always check for errors explicitly and handle them appropriately, do not ignore them.
When an error occurs in a function that returns an error, return the error immediately.

# Testing:
Generate unit tests for core functionality in a separate file ending with _test.go.
Use the standard testing package for all tests.

# Project Specifics:
The project uses Go Modules for dependency management.
All API endpoints should follow RESTful API design principles.

