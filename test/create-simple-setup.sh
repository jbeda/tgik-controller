kubectl create namespace secretsync
kubectl create secret generic ss --from-literal=user=password --namespace=secretsync
kubectl annotate --namespace=secretsync secret ss eightypercent.net/secretsync=true

kubectl annotate namespace default eightypercent.net/secretsync=true