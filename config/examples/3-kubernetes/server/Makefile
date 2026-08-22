app_name   ?= server
image_name ?= moorara/$(app_name)
image_tag  ?= latest


.PHONY: run
run:
	go build -o $(app_name); \
	trap 'rm -f $(app_name)' EXIT INT TERM; \
	./$(app_name)

.PHONY: image
image:
	GOOS=linux GOARCH=amd64 go build -o $(app_name)
	podman image build --tag $(image_name):$(image_tag) .
	podman image push $(image_name):$(image_tag)
	rm $(app_name)

.PHONY: k8s-deploy
k8s-deploy:
	kubectl create -f kubernetes.yaml

.PHONY: k8s-logs
k8s-logs:
	kubectl logs -f deployment/$(app_name)

.PHONY: k8s-update-log-level
k8s-update-log-level:
	kubectl patch configmap $(app_name) -p '{"data": {"log-level": "info"}}'

.PHONY: k8s-delete
k8s-delete:
	kubectl delete service $(app_name)
	kubectl delete deployment $(app_name)
	kubectl delete configmap $(app_name)
