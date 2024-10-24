/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package v1alpha1

import (
	"context"
	"time"

	v1alpha1 "kubedb.dev/apimachinery/apis/autoscaling/v1alpha1"
	scheme "kubedb.dev/apimachinery/client/clientset/versioned/scheme"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// FerretDBAutoscalersGetter has a method to return a FerretDBAutoscalerInterface.
// A group's client should implement this interface.
type FerretDBAutoscalersGetter interface {
	FerretDBAutoscalers(namespace string) FerretDBAutoscalerInterface
}

// FerretDBAutoscalerInterface has methods to work with FerretDBAutoscaler resources.
type FerretDBAutoscalerInterface interface {
	Create(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.CreateOptions) (*v1alpha1.FerretDBAutoscaler, error)
	Update(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.UpdateOptions) (*v1alpha1.FerretDBAutoscaler, error)
	UpdateStatus(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.UpdateOptions) (*v1alpha1.FerretDBAutoscaler, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha1.FerretDBAutoscaler, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha1.FerretDBAutoscalerList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.FerretDBAutoscaler, err error)
	FerretDBAutoscalerExpansion
}

// ferretDBAutoscalers implements FerretDBAutoscalerInterface
type ferretDBAutoscalers struct {
	client rest.Interface
	ns     string
}

// newFerretDBAutoscalers returns a FerretDBAutoscalers
func newFerretDBAutoscalers(c *AutoscalingV1alpha1Client, namespace string) *ferretDBAutoscalers {
	return &ferretDBAutoscalers{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the ferretDBAutoscaler, and returns the corresponding ferretDBAutoscaler object, and an error if there is any.
func (c *ferretDBAutoscalers) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.FerretDBAutoscaler, err error) {
	result = &v1alpha1.FerretDBAutoscaler{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of FerretDBAutoscalers that match those selectors.
func (c *ferretDBAutoscalers) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.FerretDBAutoscalerList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha1.FerretDBAutoscalerList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested ferretDBAutoscalers.
func (c *ferretDBAutoscalers) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a ferretDBAutoscaler and creates it.  Returns the server's representation of the ferretDBAutoscaler, and an error, if there is any.
func (c *ferretDBAutoscalers) Create(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.CreateOptions) (result *v1alpha1.FerretDBAutoscaler, err error) {
	result = &v1alpha1.FerretDBAutoscaler{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(ferretDBAutoscaler).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a ferretDBAutoscaler and updates it. Returns the server's representation of the ferretDBAutoscaler, and an error, if there is any.
func (c *ferretDBAutoscalers) Update(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.UpdateOptions) (result *v1alpha1.FerretDBAutoscaler, err error) {
	result = &v1alpha1.FerretDBAutoscaler{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		Name(ferretDBAutoscaler.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(ferretDBAutoscaler).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *ferretDBAutoscalers) UpdateStatus(ctx context.Context, ferretDBAutoscaler *v1alpha1.FerretDBAutoscaler, opts v1.UpdateOptions) (result *v1alpha1.FerretDBAutoscaler, err error) {
	result = &v1alpha1.FerretDBAutoscaler{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		Name(ferretDBAutoscaler.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(ferretDBAutoscaler).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the ferretDBAutoscaler and deletes it. Returns an error if one occurs.
func (c *ferretDBAutoscalers) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *ferretDBAutoscalers) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched ferretDBAutoscaler.
func (c *ferretDBAutoscalers) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.FerretDBAutoscaler, err error) {
	result = &v1alpha1.FerretDBAutoscaler{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("ferretdbautoscalers").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}
