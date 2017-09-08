package main

import (
	"log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	informercorev1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listercorev1 "k8s.io/client-go/listers/core/v1"
	apicorev1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	secretSyncAnnotation      = "eightypercent.net/secretsync"
	secretSyncSourceNamespace = "secretsync"
)

var namespaceBlacklist = map[string]bool{
	"kube-public":             true,
	"kube-system":             true,
	secretSyncSourceNamespace: true,
}

type TGIKController struct {
	secretGetter          corev1.SecretsGetter
	secretLister          listercorev1.SecretLister
	secretListerSynced    cache.InformerSynced
	namespaceGetter       corev1.NamespacesGetter
	namespaceLister       listercorev1.NamespaceLister
	namespaceListerSynced cache.InformerSynced
}

func NewTGIKController(client *kubernetes.Clientset,
	secretInformer informercorev1.SecretInformer,
	namespaceInformer informercorev1.NamespaceInformer) *TGIKController {
	c := &TGIKController{
		secretGetter:          client.CoreV1(),
		secretLister:          secretInformer.Lister(),
		secretListerSynced:    secretInformer.Informer().HasSynced,
		namespaceGetter:       client.CoreV1(),
		namespaceLister:       namespaceInformer.Lister(),
		namespaceListerSynced: namespaceInformer.Informer().HasSynced,
	}

	secretInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				c.onAdd(obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				c.onUpdate(oldObj, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				c.onDelete(obj)
			},
		},
	)

	return c
}

func (c *TGIKController) Run(stop <-chan struct{}) {
	log.Print("waiting for cache sync")
	if !cache.WaitForCacheSync(stop, c.secretListerSynced, c.namespaceListerSynced) {
		log.Print("timed out waiting for cache sync")
		return
	}
	log.Print("caches are synced")

	// wait until we're told to stop
	// log.Print("waiting for stop signal")
	// <-stop
	// log.Print("received stop signal")
	c.doSync()
}

func (c *TGIKController) onAdd(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("onAdd: error getting key for %#v: %v", obj, err)
		runtime.HandleError(err)
	}
	log.Printf("onAdd: %v", key)
	// c.handleSecretChange(obj)
}

func (c *TGIKController) onUpdate(oldObj, newObj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(oldObj)
	if err != nil {
		log.Printf("onUpdate: error getting key for %#v: %v", oldObj, err)
		runtime.HandleError(err)
	}
	log.Printf("onUpdate: %v", key)
	// c.handleSecretChange(newObj)
}

func (c *TGIKController) onDelete(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
	}
	log.Printf("onDelete: %v", key)
	// c.handleSecretChange(obj)
}

func (c *TGIKController) handleSecretChange(obj interface{}) {
	secret, ok := obj.(*apicorev1.Secret)
	if !ok {
		// TODO: this is probably a `DeletedFinalStateUnknown`.  Figure out what
		// to do.
		return
	}

	if secret.ObjectMeta.Namespace != secretSyncSourceNamespace {
		log.Printf("Skipping secret in wrong namespace")
		return
	}

	// if secret.Type != secretSyncType {
	// 	log.Printf("Skipping secret of wrong type")
	// 	return
	// }

	log.Printf("Do something with this secret")
	nsList, err := c.namespaceGetter.Namespaces().List(metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing namespaces: %v", err)
		return
	}
	for _, ns := range nsList.Items {
		nsName := ns.ObjectMeta.Name
		if _, ok := namespaceBlacklist[nsName]; ok {
			log.Printf("Skipping namespace on blacklist: %v", nsName)
			continue
		}
		log.Printf("We should copy %s to namespace %s", secret.ObjectMeta.Name, ns.ObjectMeta.Name)
		c.doSync()
	}
}

func (c *TGIKController) getSecretsInNS(ns string) ([]*apicorev1.Secret, error) {
	rawSecrets, err := c.secretLister.Secrets(ns).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var secrets []*apicorev1.Secret
	for _, secret := range rawSecrets {
		if _, ok := secret.Annotations[secretSyncAnnotation]; ok {
			secrets = append(secrets, secret)
		}
	}
	return secrets, nil
}

func (c *TGIKController) doSync() {
	log.Printf("here!")
	srcSecrets, err := c.getSecretsInNS(secretSyncSourceNamespace)
	if err != nil {
		log.Panicf("oh noes! %v", err)
	}

	rawNamespaces, err := c.namespaceLister.List(labels.Everything())
	if err != nil {
		log.Panicf("oh noes! %v", err)
	}
	var targetNamespaces []*apicorev1.Namespace
	for _, ns := range rawNamespaces {
		if _, ok := ns.Annotations[secretSyncAnnotation]; ok {
			targetNamespaces = append(targetNamespaces, ns)
		}
	}

	for _, ns := range targetNamespaces {
		c.SyncNamespace(srcSecrets, ns.Name)
	}
}

func (c *TGIKController) SyncNamespace(secrets []*apicorev1.Secret, ns string) {
	// 1. Create/Update all of the secrets in this namespace
	for _, secret := range secrets {
		newSecretInf, _ := scheme.Scheme.DeepCopy(secret)
		newSecret := newSecretInf.(*apicorev1.Secret)
		newSecret.Namespace = ns
		newSecret.ResourceVersion = ""
		newSecret.UID = ""

		_, err := c.secretGetter.Secrets(ns).Create(newSecret)
		if apierrors.IsAlreadyExists(err) {
			_, err = c.secretGetter.Secrets(ns).Update(newSecret)
		}
		if err != nil {
			log.Printf("%v", err)
		}
	}

	// 2. Delete secrets that have annotation but are not in our src list
	srcSecrets := sets.String{}
	targetSecrets := sets.String{}

	for _, secret := range secrets {
		srcSecrets.Insert(secret.Name)
	}

	targetSecretList, err := c.getSecretsInNS(ns)
	if err != nil {
		log.Printf("%v", err)
	}
	for _, secret := range targetSecretList {
		targetSecrets.Insert(secret.Name)
	}

	deleteSet := targetSecrets.Difference(srcSecrets)
	for secretName, _ := range deleteSet {
		log.Printf("Delete %v", secretName)
		err = c.secretGetter.Secrets(ns).Delete(secretName, nil)
		if err != nil {
			log.Printf("%v", err)
		}
	}
}
