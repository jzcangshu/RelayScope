package bootstrap

import (
	"context"
	"time"

	"relaypulse/internal/store"
)

type SiteSeed struct {
	Name            string
	BaseURL         string
	SourceURL       string
	Adapter         string
	AdapterConfig   string
	SessionRequired bool
}

var InitialSites = []SiteSeed{
	{Name: "Elysiver", BaseURL: "https://elysiver.h-e.top", SourceURL: "https://elysiver.h-e.top/pricing", Adapter: "newapi-pricing"},
	{Name: "君の公益", BaseURL: "https://muyuan.do", SourceURL: "https://muyuan.do/pricing", Adapter: "newapi-pricing", AdapterConfig: `{"availabilityMode":"presence","skipDetails":true,"pricingRequiresSession":true}`, SessionRequired: true},
	{Name: "new-api abrdns", BaseURL: "https://new-api.abrdns.com", SourceURL: "https://new-api.abrdns.com/status", Adapter: "newapi-probe", AdapterConfig: `{"pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}`, SessionRequired: true},
	{Name: "chybenzun", BaseURL: "https://status.chybenzun.top", SourceURL: "https://status.chybenzun.top/", Adapter: "custom-probe"},
	{Name: "metapi", BaseURL: "https://metapi.lilililwan.xyz", SourceURL: "https://metapi.lilililwan.xyz/pricing", Adapter: "newapi-pricing"},
	{Name: "42w", BaseURL: "https://api.42w.shop", SourceURL: "https://api.42w.shop/pricing", Adapter: "newapi-pricing", SessionRequired: true},
	{Name: "fengwind", BaseURL: "https://api.fengwind.com", SourceURL: "https://api.fengwind.com/model-market", Adapter: "model-market", AdapterConfig: `{"pricingAdapter":"model-market"}`, SessionRequired: true},
	{Name: "古德科技", BaseURL: "http://v4.whyyin.cn:28327", SourceURL: "http://v4.whyyin.cn:28327/pricing", Adapter: "newapi-pricing"},
	{Name: "月城公益站", BaseURL: "https://52ccl.net", SourceURL: "https://52ccl.net/pricing", Adapter: "newapi-pricing", SessionRequired: true},
	{Name: "霸气公益平台", BaseURL: "https://ai.121628.xyz", SourceURL: "https://ai.121628.xyz/pricing", Adapter: "newapi-pricing", SessionRequired: true},
	{Name: "感恩公益站", BaseURL: "https://aitoken.forum", SourceURL: "https://aitoken.forum/pricing", Adapter: "newapi-pricing"},
	{Name: "南梁 API", BaseURL: "https://api.llm.pm", SourceURL: "https://api.llm.pm/pricing", Adapter: "newapi-pricing"},
	{Name: "Muu OpenAPI", BaseURL: "https://demo.dev2.mulink.top", SourceURL: "https://demo.dev2.mulink.top/pricing", Adapter: "newapi-pricing"},
	{Name: "V - API", BaseURL: "https://v-api.de5.net", SourceURL: "https://v-api.de5.net/pricing", Adapter: "newapi-pricing", SessionRequired: true},
	{Name: "Yetoken", BaseURL: "https://yetoken.cc.cd", SourceURL: "https://yetoken.cc.cd/pricing", Adapter: "newapi-pricing"},
	{Name: "CN Gov", BaseURL: "https://cngov.cc.cd", SourceURL: "https://cngov.cc.cd/pricing", Adapter: "newapi-pricing"},
	{Name: "Joverna", BaseURL: "https://jiuuij.de5.net", SourceURL: "https://jiuuij.de5.net/model-health", Adapter: "newapi-pricing", AdapterConfig: `{"skipDetails":true}`},
	{Name: "Hlool API", BaseURL: "https://api.hlool.top", SourceURL: "https://api.hlool.top/pricing", Adapter: "newapi-pricing"},
	{Name: "薄荷 API", BaseURL: "https://x666.me", SourceURL: "https://x666.me/", Adapter: "newapi-probe", AdapterConfig: `{"statusBaseUrl":"https://tool.x666.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true}`},
	{Name: "HXI AI", BaseURL: "https://runanytime.hxi.me", SourceURL: "https://stat.hxi.me/status/ai", Adapter: "uptime-kuma", AdapterConfig: `{"statusBaseUrl":"https://stat.hxi.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}`},
	{Name: "jianzhile.vip", BaseURL: "https://jianzhile.vip", SourceURL: "https://jianzhile.vip/console/model-status", Adapter: "model-probe", AdapterConfig: `{"modelProbePath":"/api/model_probe/status","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`, SessionRequired: true},
	{Name: "Pigeon Sub2API", BaseURL: "https://sub2.pigeonw.com", SourceURL: "https://sub2.pigeonw.com/monitor", Adapter: "sub2api-monitor", AdapterConfig: `{"monitorPath":"/api/v1/channel-monitors"}`, SessionRequired: true},
	{Name: "老魔公益站", BaseURL: "https://api.2020111.xyz", SourceURL: "https://api.2020111.xyz/pricing", Adapter: "newapi-pricing"},
	{Name: "PM-API", BaseURL: "https://xn--wnup5g6so4wn.de5.net", SourceURL: "https://xn--wnup5g6so4wn.de5.net/pricing", Adapter: "newapi-pricing"},
	{Name: "Ark API", BaseURL: "https://windhub.cc", SourceURL: "https://windhub.cc/model-status", Adapter: "newapi-probe", AdapterConfig: `{"catalogPath":"/api/enhancements/model-status/embed/status/all","statusPath":"","detailPath":"","detailPathTemplate":"/api/enhancements/model-status/embed/status/{model}?window=24h","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`},
	{Name: "773 公益站", BaseURL: "https://api-yi-hydrogel.seeseed1ck.icu", SourceURL: "https://api-yi-hydrogel.seeseed1ck.icu/pricing", Adapter: "newapi-pricing"},
	{Name: "AIAPI", BaseURL: "https://aiapi.exe.xyz", SourceURL: "https://aiapi.exe.xyz/jk", Adapter: "aiapi-probe", AdapterConfig: `{"statusPath":"/jk/api/status","period":"24h","board":"hot"}`},
	{Name: "X-API", BaseURL: "https://x-api.cfd", SourceURL: "https://x-api.cfd/pool", Adapter: "xapi-pool"},
	{Name: "小鸡毛的公益API站", BaseURL: "https://api.ark717.com", SourceURL: "https://api.ark717.com/pricing", Adapter: "newapi-pricing"},
	{Name: "PrismAI", BaseURL: "https://ai.prism.uno", SourceURL: "https://ai.prism.uno/pricing", Adapter: "newapi-pricing"},
	{Name: "dudu公益站", BaseURL: "https://wududu.edu.kg", SourceURL: "https://wududu.edu.kg/pricing", Adapter: "newapi-pricing"},
	{Name: "CoeeApi", BaseURL: "https://status.coee.ccwu.cc", SourceURL: "https://api.coee.ccwu.cc/", Adapter: "uptime-kuma", AdapterConfig: `{"slug":"check","retryAttempts":3,"monitorNameMode":"suffix-model"}`},
	{Name: "luckyg", BaseURL: "https://luckyg.131518.xyz", SourceURL: "https://luckyg.131518.xyz/pricing", Adapter: "newapi-pricing"},
	{Name: "Pavv", BaseURL: "https://pavv.me", SourceURL: "https://pavv.me/pricing", Adapter: "newapi-pricing"},
	{Name: "imagic", BaseURL: "https://newapi.imagic.eu.org", SourceURL: "https://newapi.imagic.eu.org/pricing", Adapter: "newapi-pricing"},
	{Name: "咕咕嘎嘎", BaseURL: "https://api.fengshao1227.com", SourceURL: "https://api.fengshao1227.com/status", Adapter: "model-pulse", AdapterConfig: `{"pulsePath":"/api/model-pulse","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`, SessionRequired: true},
}

func EnsureInitialSites(ctx context.Context, dbStore *store.Store) error {
	sites, err := dbStore.ListAllSites(ctx)
	if err != nil {
		return err
	}
	existing := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		existing[site.BaseURL] = struct{}{}
	}
	for _, seed := range InitialSites {
		if _, exists := existing[seed.BaseURL]; exists {
			continue
		}
		if _, err := dbStore.CreateSite(ctx, store.Site{
			Name: seed.Name, BaseURL: seed.BaseURL, SourceURL: seed.SourceURL, AdapterKey: seed.Adapter,
			AdapterConfig: defaultAdapterConfig(seed.AdapterConfig), Enabled: true, SessionRequired: seed.SessionRequired, Interval: 15 * time.Minute, Jitter: 2 * time.Minute,
		}); err != nil {
			return err
		}
	}
	return nil
}

func defaultAdapterConfig(value string) string {
	if value == "" {
		return `{}`
	}
	return value
}
