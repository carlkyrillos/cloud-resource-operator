package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/integr8ly/cloud-resource-operator/internal/k8sutil"
	moqClient "github.com/integr8ly/cloud-resource-operator/pkg/client/fake"
	"github.com/integr8ly/cloud-resource-operator/pkg/providers"
	"github.com/spf13/afero"

	configv1 "github.com/openshift/api/config/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	controllerruntime "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var configMapNameSpace, _ = k8sutil.GetWatchNamespace()

func newFakeInfrastructure() *configv1.Infrastructure {
	return &configv1.Infrastructure{
		ObjectMeta: controllerruntime.ObjectMeta{
			Name: "cluster",
		},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "test",
			PlatformStatus: &configv1.PlatformStatus{
				Type: configv1.AWSPlatformType,
				AWS: &configv1.AWSPlatformStatus{
					Region: "test-region",
				},
			},
		},
	}
}

func TestNewConfigManager(t *testing.T) {
	cases := []struct {
		name              string
		cmName            string
		expectedName      string
		cmNamespace       string
		expectedNamespace string
		client            client.Client
	}{
		{
			name:              "test defaults are set when empty strings are provided",
			cmName:            "",
			cmNamespace:       "",
			expectedName:      "cloud-resources-aws-strategies",
			expectedNamespace: configMapNameSpace,
			client:            nil,
		},
		{
			name:              "test defaults are not used when non-empty strings are provided",
			cmName:            "test",
			cmNamespace:       "test",
			expectedName:      "test",
			expectedNamespace: "test",
			client:            nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := NewConfigMapConfigManager(tc.cmName, tc.cmNamespace, tc.client)
			if cm.configMapName != tc.expectedName {
				t.Fatalf("unexpected name, expected %s but got %s", tc.expectedName, cm.configMapName)
			}
			if cm.configMapNamespace != tc.expectedNamespace {
				t.Fatalf("unexpected namespace, expected %s but got %s", tc.expectedNamespace, cm.configMapNamespace)
			}
		})
	}
}

func TestConfigManager_ReadBlobStorageStrategy(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	sc := &StrategyConfig{
		Region:         "eu-west-1",
		CreateStrategy: json.RawMessage("{\"bucket\":\"testbucket\"}"),
	}
	rawStratCfg, err := json.Marshal(sc)
	if err != nil {
		t.Fatal("failed to marshal strategy config", err)
	}
	fakeClient := moqClient.NewSigsClientMoqWithScheme(scheme, &v1.ConfigMap{
		ObjectMeta: controllerruntime.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Data: map[string]string{
			"blobstorage": fmt.Sprintf("{\"test\": %s}", string(rawStratCfg)),
		},
	})
	cases := []struct {
		name                string
		cmName              string
		cmNamespace         string
		rt                  providers.ResourceType
		tier                string
		expectedRegion      string
		expectedRawStrategy string
		client              client.Client
		expectErr           bool
	}{
		{
			name:                "test strategy is parsed successfully when tier exists",
			cmName:              "test",
			cmNamespace:         "test",
			rt:                  providers.BlobStorageResourceType,
			tier:                "test",
			expectedRegion:      "eu-west-1",
			expectedRawStrategy: string(sc.CreateStrategy),
			client:              fakeClient,
		},
		{
			name:        "test error is returned when strategy does not exist for tier",
			cmName:      "test",
			cmNamespace: "test",
			rt:          providers.BlobStorageResourceType,
			tier:        "doesnotexist",
			expectErr:   true,
			client:      fakeClient,
		},
		{
			name:        "aws strategy config map not found should return default strategy",
			cmName:      "test",
			cmNamespace: "test",
			rt:          providers.BlobStorageResourceType,
			tier:        "doesnotexist",
			expectErr:   true,
			client:      moqClient.NewSigsClientMoqWithScheme(scheme),
		},
		{
			name:        "aws strategy for resource type is not defined",
			cmName:      "test",
			cmNamespace: "test",
			rt:          providers.BlobStorageResourceType,
			tier:        "test",
			expectErr:   true,
			client: moqClient.NewSigsClientMoqWithScheme(scheme, &v1.ConfigMap{
				ObjectMeta: controllerruntime.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string]string{
					"invalidType": fmt.Sprintf("{\"test\": %s}", string(rawStratCfg)),
				},
			}),
		},
		{
			name:        "failed to unmarshal strategy mapping",
			cmName:      "test",
			cmNamespace: "test",
			rt:          providers.BlobStorageResourceType,
			tier:        "test",
			expectErr:   true,
			client: moqClient.NewSigsClientMoqWithScheme(scheme, &v1.ConfigMap{
				ObjectMeta: controllerruntime.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string]string{
					"blobstorage": "{\"test\":{\"region\":666}}",
				},
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := NewConfigMapConfigManager(tc.cmName, tc.cmNamespace, tc.client)
			sc, err := cm.ReadStorageStrategy(context.TODO(), tc.rt, tc.tier)
			if err != nil {
				if tc.expectErr {
					return
				}
				t.Fatal("unexpected error", err)
			}
			if sc.Region != tc.expectedRegion {
				t.Fatalf("unexpected region, expected %s but got %s", tc.expectedRegion, sc.Region)
			}
			if string(sc.CreateStrategy) != tc.expectedRawStrategy {
				t.Fatalf("unexpected raw strategy, expected %s but got %s", tc.expectedRawStrategy, sc.CreateStrategy)
			}
		})
	}
}

func TestGetRegionFromStrategyOrDefault(t *testing.T) {
	fakeScheme := runtime.NewScheme()
	v1.AddToScheme(fakeScheme)
	configv1.Install(fakeScheme)

	fakeStrategy := &StrategyConfig{
		Region: "strategy-region",
	}
	fakeInfra := newFakeInfrastructure()

	type args struct {
		ctx      context.Context
		c        client.Client
		strategy *StrategyConfig
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "fail to get default region",
			args: args{
				ctx:      context.TODO(),
				c:        moqClient.NewSigsClientMoqWithScheme(fakeScheme),
				strategy: fakeStrategy,
			},
			wantErr: true,
		},
		{
			name: "strategy defines region",
			args: args{
				ctx:      context.TODO(),
				c:        moqClient.NewSigsClientMoqWithScheme(fakeScheme, fakeInfra),
				strategy: fakeStrategy,
			},
			want: fakeStrategy.Region,
		},
		{
			name: "default used when strategy does not define region",
			args: args{
				ctx: context.TODO(),
				c:   moqClient.NewSigsClientMoqWithScheme(fakeScheme, fakeInfra),
				strategy: &StrategyConfig{
					Region: "",
				},
			},
			want: fakeInfra.Status.PlatformStatus.AWS.Region,
		},
		{
			name: "failed to retrieve region from cluster, region is not defined",
			args: args{
				ctx: context.TODO(),
				c: moqClient.NewSigsClientMoqWithScheme(fakeScheme, &configv1.Infrastructure{
					ObjectMeta: controllerruntime.ObjectMeta{
						Name: "cluster",
					},
					Status: configv1.InfrastructureStatus{
						InfrastructureName: "test",
						PlatformStatus: &configv1.PlatformStatus{
							Type: configv1.AWSPlatformType,
							AWS:  &configv1.AWSPlatformStatus{},
						},
					},
				}),
				strategy: &StrategyConfig{
					Region: "",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetRegionFromStrategyOrDefault(tt.args.ctx, tt.args.c, tt.args.strategy)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetRegionFromStrategyOrDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetRegionFromStrategyOrDefault() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateSessionFromStrategy(t *testing.T) {
	fakeScheme := runtime.NewScheme()
	err := configv1.Install(fakeScheme)
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	fakeStrategy := &StrategyConfig{
		Region: "strategy-region",
	}
	fakeInfra := newFakeInfrastructure()
	type args struct {
		ctx      context.Context
		c        client.Client
		cred     *Credentials
		strategy *StrategyConfig
		mockFs   func()
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "fail to get default region",
			args: args{
				ctx:    context.TODO(),
				c:      moqClient.NewSigsClientMoqWithScheme(fakeScheme),
				mockFs: func() {},
			},
			wantErr: true,
		},
		{
			name: "create aws session with sts idp - local",
			args: args{
				ctx:      context.TODO(),
				c:        moqClient.NewSigsClientMoqWithScheme(fakeScheme, fakeInfra),
				strategy: fakeStrategy,
				cred: &Credentials{
					RoleArn:       "ROLE_ARN",
					TokenFilePath: "TOKEN_FILE_PATH",
				},
				mockFs: func() {},
			},
		},
		{
			name: "create aws session with sts idp - in pod",
			args: args{
				ctx:      context.TODO(),
				c:        moqClient.NewSigsClientMoqWithScheme(fakeScheme, fakeInfra),
				strategy: fakeStrategy,
				cred: &Credentials{
					RoleArn:       "ROLE_ARN",
					TokenFilePath: "TOKEN_FILE_PATH",
				},
				mockFs: func() {
					// Mock filesystem
					k8sutil.AppFS = afero.NewMemMapFs()
					if err := k8sutil.AppFS.MkdirAll("/var/run/secrets/kubernetes.io", 0755); err != nil {
						t.Fatal(err)
					}
					if err := afero.WriteFile(k8sutil.AppFS, "/var/run/secrets/kubernetes.io/serviceaccount", []byte("a file"), 0755); err != nil {
						t.Fatal(err)
					}
				},
			},
		},
		{
			name: "create aws session with static idp",
			args: args{
				ctx:      context.TODO(),
				c:        moqClient.NewSigsClientMoqWithScheme(fakeScheme, fakeInfra),
				strategy: fakeStrategy,
				cred: &Credentials{
					AccessKeyID:     "ACCESS_KEY_ID",
					SecretAccessKey: "SECRET_ACCESS_KEY",
				},
				mockFs: func() {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.args.mockFs()
			// Reset
			defer func() {
				k8sutil.AppFS = afero.NewOsFs()
			}()
			got, err := CreateSessionFromStrategy(tt.args.ctx, tt.args.c, tt.args.cred, tt.args.strategy)
			if tt.wantErr {
				if !errorContains(err, "failed to get region") {
					t.Fatalf("unexpected error from CreateSessionFromStrategy(): %v", err)
				}
				return
			}
			cred, _ := got.Config.Credentials.Get()
			switch tt.args.cred.RoleArn {
			case "ROLE_ARN":
				if k8sutil.IsRunModeLocal() {
					if cred.ProviderName != "AssumeRoleProvider" {
						t.Fatalf("aws session with sts assume role provider credentials not created properly")
					}
				} else {
					if cred.ProviderName != "" {
						t.Fatalf("aws session with sts credentials not created properly")
					}
				}
			default:
				if cred.ProviderName != "StaticProvider" {
					t.Fatalf("aws session with static credentials not created properly")
				}
			}
		})
	}
}

func TestNewDefaultConfigMapConfigManager(t *testing.T) {
	fakeScheme := runtime.NewScheme()
	err := configv1.Install(fakeScheme)
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	type args struct {
		c client.Client
	}
	tests := []struct {
		name              string
		args              args
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "successfully create new default config map manager",
			args: args{
				c: moqClient.NewSigsClientMoqWithScheme(fakeScheme),
			},
			expectedName:      DefaultConfigMapName,
			expectedNamespace: DefaultConfigMapNamespace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewDefaultConfigMapConfigManager(tt.args.c)
			if cm.configMapName != tt.expectedName {
				t.Fatalf("unexpected name, expected %s but got %s", tt.expectedName, cm.configMapName)
			}
			if cm.configMapNamespace != tt.expectedNamespace {
				t.Fatalf("unexpected namespace, expected %s but got %s", tt.expectedNamespace, cm.configMapNamespace)
			}
		})
	}
}

func errorContains(out error, want string) bool {
	if out == nil {
		return want == ""
	}
	if want == "" {
		return false
	}
	return strings.Contains(out.Error(), want)
}
