/*
Copyright 2020 The Kubernetes Authors.

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

package filters

import (
	"errors"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/storageversion"
	_ "k8s.io/component-base/metrics/prometheus/workqueue" // for workqueue metric registration
	"k8s.io/klog/v2"
)

// WithStorageVersionPrecondition checks if the storage version barrier has
// completed, if not, it only passes the following API requests:
// 1. non-resource requests,
// 2. read requests,
// 3. write requests to the storageversion API,
// 4. create requests to the namespace API sent by apiserver itself,
// 5. write requests to the lease API in kube-system namespace,
// 6. resources whose StorageVersion is not pending update, including non-persisted resources.
// 在对资源对象进行操作之前检查其存储版本，如果版本不匹配，只放行下面几种请求。
// 1. 非资源类请求
// 2. 读请求
// 3. 操作 storageversion API
// 4. api server 发的，对于 namespace 的创建请求
// 5. 往 kube-system 写租约相关的 API
// 6. 存储版本已经是最新或者不需要更新的资源对象，比如非持久化资源，如
func WithStorageVersionPrecondition(handler http.Handler, svm storageversion.Manager, s runtime.NegotiatedSerializer) http.Handler {
	if svm == nil {
		// TODO(roycaihw): switch to warning after the feature graduate to beta/GA
		klog.V(2).Infof("Storage Version barrier is disabled")
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if svm.Completed() {
			// 如果匹配，放行
			handler.ServeHTTP(w, req)
			return
		}
		ctx := req.Context()
		requestInfo, found := request.RequestInfoFrom(ctx)
		if !found {
			responsewriters.InternalError(w, req, errors.New("no RequestInfo found in the context"))
			return
		}
		// Allow non-resource requests
		// 非资源类请求,放行
		if !requestInfo.IsResourceRequest {
			handler.ServeHTTP(w, req)
			return
		}
		// Allow read requests
		// 读请求，放行
		if requestInfo.Verb == "get" || requestInfo.Verb == "list" || requestInfo.Verb == "watch" {
			handler.ServeHTTP(w, req)
			return
		}
		// Allow writes to the storage version API
		// 操作 storageversion API， 放行
		if requestInfo.APIGroup == "internal.apiserver.k8s.io" && requestInfo.Resource == "storageversions" {
			handler.ServeHTTP(w, req)
			return
		}
		// The system namespace is required for apiserver-identity lease to exist. Allow the apiserver
		// itself to create namespaces.
		// NOTE: with this exception, if the bootstrap client writes namespaces with a new version,
		// and the upgraded apiserver dies before updating the StorageVersion for namespaces, the
		// storage migrator won't be able to tell these namespaces are stored in a different version in etcd.
		// Because the bootstrap client only creates system namespace and doesn't update them, this can
		// only happen if the upgraded apiserver is the first apiserver that kicks off namespace creation,
		// or if an upgraded server that joins an existing cluster has new system namespaces (other
		// than kube-system, kube-public, kube-node-lease) that need to be created.
		u, hasUser := request.UserFrom(ctx)
		// 判断是 api server 发的，对于 namespace 的创建请求，放行
		if requestInfo.APIGroup == "" && requestInfo.Resource == "namespaces" &&
			requestInfo.Verb == "create" && hasUser &&
			u.GetName() == user.APIServerUser && contains(u.GetGroups(), user.SystemPrivilegedGroup) {
			handler.ServeHTTP(w, req)
			return
		}
		// Allow writes to the lease API in kube-system. The storage version API depends on the
		// apiserver-identity leases to operate. Leases in kube-system are either apiserver-identity
		// lease (which gets garbage collected when stale) or leader-election leases (which gets
		// periodically updated by system components). Both types of leases won't be stale in etcd.
		// 往 kube-system 写租约相关的 API, 放行
		if requestInfo.APIGroup == "coordination.k8s.io" && requestInfo.Resource == "leases" &&
			requestInfo.Namespace == metav1.NamespaceSystem {
			handler.ServeHTTP(w, req)
			return
		}
		// If the resource's StorageVersion is not in the to-be-updated list, let it pass.
		// Non-persisted resources are not in the to-be-updated list, so they will pass.
		// 存储版本已经是最新或者不需要更新的资源对象，对这些资源对象的写操作可以被放行，不受存储版本屏障的限制。
		gr := schema.GroupResource{requestInfo.APIGroup, requestInfo.Resource}
		if !svm.PendingUpdate(gr) {
			handler.ServeHTTP(w, req)
			return
		}

		gv := schema.GroupVersion{requestInfo.APIGroup, requestInfo.APIVersion}
		responsewriters.ErrorNegotiated(apierrors.NewServiceUnavailable(fmt.Sprintf("wait for storage version registration to complete for resource: %v, last seen error: %v", gr, svm.LastUpdateError(gr))), s, gv, w, req)
	})
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
