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

	v1alpha1 "kubedb.dev/apimachinery/apis/ops/v1alpha1"
	scheme "kubedb.dev/apimachinery/client/clientset/versioned/scheme"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// RedisSentinelOpsRequestsGetter has a method to return a RedisSentinelOpsRequestInterface.
// A group's client should implement this interface.
type RedisSentinelOpsRequestsGetter interface {
	RedisSentinelOpsRequests(namespace string) RedisSentinelOpsRequestInterface
}

// RedisSentinelOpsRequestInterface has methods to work with RedisSentinelOpsRequest resources.
type RedisSentinelOpsRequestInterface interface {
	Create(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.CreateOptions) (*v1alpha1.RedisSentinelOpsRequest, error)
	Update(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.UpdateOptions) (*v1alpha1.RedisSentinelOpsRequest, error)
	UpdateStatus(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.UpdateOptions) (*v1alpha1.RedisSentinelOpsRequest, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha1.RedisSentinelOpsRequest, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha1.RedisSentinelOpsRequestList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.RedisSentinelOpsRequest, err error)
	RedisSentinelOpsRequestExpansion
}

// redisSentinelOpsRequests implements RedisSentinelOpsRequestInterface
type redisSentinelOpsRequests struct {
	client rest.Interface
	ns     string
}

// newRedisSentinelOpsRequests returns a RedisSentinelOpsRequests
func newRedisSentinelOpsRequests(c *OpsV1alpha1Client, namespace string) *redisSentinelOpsRequests {
	return &redisSentinelOpsRequests{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the redisSentinelOpsRequest, and returns the corresponding redisSentinelOpsRequest object, and an error if there is any.
func (c *redisSentinelOpsRequests) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.RedisSentinelOpsRequest, err error) {
	result = &v1alpha1.RedisSentinelOpsRequest{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of RedisSentinelOpsRequests that match those selectors.
func (c *redisSentinelOpsRequests) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.RedisSentinelOpsRequestList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha1.RedisSentinelOpsRequestList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested redisSentinelOpsRequests.
func (c *redisSentinelOpsRequests) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a redisSentinelOpsRequest and creates it.  Returns the server's representation of the redisSentinelOpsRequest, and an error, if there is any.
func (c *redisSentinelOpsRequests) Create(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.CreateOptions) (result *v1alpha1.RedisSentinelOpsRequest, err error) {
	result = &v1alpha1.RedisSentinelOpsRequest{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(redisSentinelOpsRequest).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a redisSentinelOpsRequest and updates it. Returns the server's representation of the redisSentinelOpsRequest, and an error, if there is any.
func (c *redisSentinelOpsRequests) Update(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.UpdateOptions) (result *v1alpha1.RedisSentinelOpsRequest, err error) {
	result = &v1alpha1.RedisSentinelOpsRequest{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		Name(redisSentinelOpsRequest.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(redisSentinelOpsRequest).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *redisSentinelOpsRequests) UpdateStatus(ctx context.Context, redisSentinelOpsRequest *v1alpha1.RedisSentinelOpsRequest, opts v1.UpdateOptions) (result *v1alpha1.RedisSentinelOpsRequest, err error) {
	result = &v1alpha1.RedisSentinelOpsRequest{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		Name(redisSentinelOpsRequest.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(redisSentinelOpsRequest).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the redisSentinelOpsRequest and deletes it. Returns an error if one occurs.
func (c *redisSentinelOpsRequests) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *redisSentinelOpsRequests) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched redisSentinelOpsRequest.
func (c *redisSentinelOpsRequests) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.RedisSentinelOpsRequest, err error) {
	result = &v1alpha1.RedisSentinelOpsRequest{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("redissentinelopsrequests").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}
