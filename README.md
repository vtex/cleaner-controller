[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=vtex_cleaner-controller&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=vtex_cleaner-controller)

# Cleaner Controller

## Development Cheat Sheet

Introductory reading:
- https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/
- https://book.kubebuilder.io/introduction.html

### CRD Generation

- Edit the [go structs](./api/v1alpha1/conditionalttl_types.go)
- Generate code and manifests:
	```bash
	make generate manifests
	```
- (Optional) Install CRDs
	```bash
	make install
	```

### Reconcile logic

- Edit the [controller code](./controllers/conditionalttl_controller.go)
- Run tests
	```bash
	make test
	```
	- Check code coverage
	```bash
	go tool cover -html=cover.out
	```
- Run controller locally (uses local k8s context authorization)
	```bash
	make run
	```
	
