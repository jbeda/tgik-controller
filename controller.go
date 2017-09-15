package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	informercorev1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listercorev1 "k8s.io/client-go/listers/core/v1"
	apicorev1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	secretSyncAnnotation      = "eightypercent.net/secretsync"
	secretSyncSourceNamespace = "secretsync"
	secretSyncKey             = "do it"
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

	queue workqueue.RateLimitingInterface
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
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "secretsync"),
	}

	// TODO: only schedule sync if it is a secret that has or had our
	// annotation.
	secretInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				log.Print("secret added")
				c.ScheduleSecretSync()
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				log.Print("secret updated")
				c.ScheduleSecretSync()
			},
			DeleteFunc: func(obj interface{}) {
				log.Print("secret deleted")
				c.ScheduleSecretSync()
			},
		},
	)

	// TODO: only schedule sync if it is a namespace that has or had our
	// annotation or the secretsync source namespace.
	namespaceInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				log.Print("namespace added")
				c.ScheduleSecretSync()
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				log.Print("namespace updated")
				c.ScheduleSecretSync()
			},
			DeleteFunc: func(obj interface{}) {
				log.Print("namespace deleted")
				c.ScheduleSecretSync()
			},
		},
	)
	return c
}

func (c *TGIKController) Run(stop <-chan struct{}) {
	var wg sync.WaitGroup

	defer func() {
		// make sure the work queue is shut down which will trigger workers to end
		log.Print("shutting down queue")
		c.queue.ShutDown()

		// wait on the workers
		log.Print("shutting down workers")
		wg.Wait()

		log.Print("workers are all done")
	}()

	log.Print("waiting for cache sync")
	if !cache.WaitForCacheSync(
		stop,
		c.secretListerSynced,
		c.namespaceListerSynced) {
		log.Print("timed out waiting for cache sync")
		return
	}
	log.Print("caches are synced")

	go func() {
		// runWorker will loop until "something bad" happens. wait.Until will
		// then rekick the worker after one second.
		wait.Until(c.runWorker, time.Second, stop)
		// tell the WaitGroup this worker is done
		wg.Done()
	}()

	// wait until we're told to stop
	log.Print("waiting for stop signal")
	<-stop
	log.Print("received stop signal")
}

func (c *TGIKController) runWorker() {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false
// when it's time to quit.
func (c *TGIKController) processNextWorkItem() bool {
	// pull the next work item from queue.  It should be a key we use to lookup
	// something in a cache
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(key)

	// do your work on the key.  This method will contains your "do stuff" logic
	err := c.doSync()
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// key. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(key)
		return true
	}

	// there was a failure so be sure to report it.  This method allows for
	// pluggable error handling which can be used for things like
	// cluster-monitoring
	runtime.HandleError(fmt.Errorf("doSync failed with: %v", err))

	// since we failed, we should requeue the item to work on later.  This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	c.queue.AddRateLimited(key)

	return true
}

func (c *TGIKController) ScheduleSecretSync() {
	c.queue.Add(secretSyncKey)
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

func (c *TGIKController) doSync() error {
	log.Printf("Starting doSync")
	srcSecrets, err := c.getSecretsInNS(secretSyncSourceNamespace)
	if err != nil {
		return err
	}

	rawNamespaces, err := c.namespaceLister.List(labels.Everything())
	if err != nil {
		return err
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

	log.Print("Finishing doSync")
	return err
}

func (c *TGIKController) SyncNamespace(secrets []*apicorev1.Secret, ns string) {
	// 1. Create/Update all of the secrets in this namespace
	for _, secret := range secrets {
		newSecretInf, _ := scheme.Scheme.DeepCopy(secret)
		newSecret := newSecretInf.(*apicorev1.Secret)
		newSecret.Namespace = ns
		newSecret.ResourceVersion = ""
		newSecret.UID = ""

		log.Printf("Creating %v/%v", ns, secret.Name)
		_, err := c.secretGetter.Secrets(ns).Create(newSecret)
		if apierrors.IsAlreadyExists(err) {
			log.Printf("Scratch that, updating %v/%v", ns, secret.Name)
			_, err = c.secretGetter.Secrets(ns).Update(newSecret)
		}
		if err != nil {
			log.Printf("Error adding secret %v/%v: %v", ns, secret.Name, err)
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
		log.Printf("Error listing secrets in %v: %v", ns, err)
	}
	for _, secret := range targetSecretList {
		targetSecrets.Insert(secret.Name)
	}

	deleteSet := targetSecrets.Difference(srcSecrets)
	for secretName, _ := range deleteSet {
		log.Printf("Delete %v/%v", ns, secretName)
		err = c.secretGetter.Secrets(ns).Delete(secretName, nil)
		if err != nil {
			log.Printf("Error deleting %v/%v: %v", ns, secretName, err)
		}
	}
}
