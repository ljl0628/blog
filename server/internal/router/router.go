// Package router 装配 Gin 引擎：全局中间件与 admin / portal 两组路由。
package router

import (
	"blog/server/internal/api"
	"blog/server/internal/config"
	"blog/server/internal/middleware"
	"blog/server/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	_ "blog/server/docs" // swag init 产物：注册 OpenAPI 文档供 gin-swagger 读取
)

// Deps 路由装配依赖。
type Deps struct {
	Cfg      *config.Config
	Log      *zap.Logger
	Auth     *service.AuthService
	Tasks    *service.TaskService
	Articles *service.ArticleService
	Portal   *service.PortalService
	Rag      *service.RagService
	Metrics  *service.MetricsService
	RagEval  *service.RagEvalService
}

// New 构建 gin 引擎与全部路由：
//
//	/api/v1/auth/login                     公开
//	/api/v1/auth/username|password         admin 组，JWT 保护（账户自助维护）
//	/api/v1/tasks|articles|...             admin 组，JWT 保护
//	/api/v1/portal/*                       前台只读，公开
func New(d Deps) *gin.Engine {
	if d.Cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(middleware.Recover(d.Log), middleware.ZapLogger(d.Log), middleware.CORS())

	// Prometheus 指标端点：供 Prometheus 抓取（blog_mq_* 等）。
	// 与业务同端口暴露；生产环境建议由网关/防火墙限制来源。
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Swagger/OpenAPI 文档：UI 在 /swagger/index.html，原始 JSON 在 /swagger/doc.json。
	// 供 Apifox 等工具按 URL 导入；生产环境如不想暴露接口文档，可由网关屏蔽 /swagger 前缀。
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	authHandler := api.NewAuthHandler(d.Auth)
	taskHandler := api.NewTaskHandler(d.Tasks)
	articleHandler := api.NewArticleHandler(d.Articles)
	portalHandler := api.NewPortalHandler(d.Portal)
	// RAG 问答：每 IP 每分钟 10 次（0 取默认值）
	ragHandler := api.NewRagHandler(d.Rag, 0, d.Log)
	metricsHandler := api.NewMetricsHandler(d.Metrics)
	ragEvalHandler := api.NewRagEvalHandler(d.RagEval)

	v1 := r.Group("/api/v1")

	// 登录（公开）
	v1.POST("/auth/login", authHandler.Login)

	// 管理端（JWT 保护）
	admin := v1.Group("")
	admin.Use(middleware.JWTAuth(d.Auth))
	{
		admin.POST("/tasks", taskHandler.Create)
		admin.GET("/tasks", taskHandler.List)
		admin.GET("/tasks/:id", taskHandler.Get)
		admin.POST("/tasks/:id/retry", taskHandler.Retry)
		admin.POST("/tasks/:id/cancel", taskHandler.Cancel)
		admin.DELETE("/tasks/:id", taskHandler.Delete)

		admin.GET("/articles", articleHandler.List)
		admin.GET("/articles/:id", articleHandler.Get)
		admin.PUT("/articles/:id", articleHandler.Update)
		admin.POST("/articles/:id/publish", articleHandler.Publish)
		admin.POST("/articles/:id/offline", articleHandler.Offline)
		admin.POST("/articles/:id/regenerate-figures", articleHandler.RegenerateFigures)
		// 单张配图按指令 AI 修改（封面除外，随正文重新生成）：
		// modify 只出预览不落库，用户确认后 apply 才更新文章
		admin.POST("/articles/:id/figures/:figureId/modify", articleHandler.ModifyFigure)
		admin.POST("/articles/:id/figures/:figureId/apply", articleHandler.ApplyFigure)
		// 文章 LLM 评审记录：管理端审核参考（只读，最新 10 条）
		admin.GET("/articles/:id/reviews", articleHandler.GetReviews)

		// profile：与登录共用 AuthHandler，走 admin JWT 组
		admin.GET("/auth/profile", authHandler.Profile)
		admin.PUT("/auth/profile", authHandler.UpdateProfile)
		// 账户自助维护：改用户名（重签 JWT）、改密码（验证旧密码）
		admin.PUT("/auth/username", authHandler.UpdateUsername)
		admin.PUT("/auth/password", authHandler.ChangePassword)

		// RAG 向量索引管理：总览/检索测试（只读）+ 全量重建
		admin.GET("/rag/index", ragHandler.Index)
		admin.POST("/rag/index/rebuild", ragHandler.Rebuild)
		admin.GET("/rag/probe", ragHandler.Probe)
		// 无命中问题列表：选题回流（管理端）
		admin.GET("/rag/unanswered", ragHandler.ListUnanswered)

		// LLM 用量看板：总览 + 按日 × 场景聚合（只读，数据源 llm_usage）
		admin.GET("/metrics/summary", metricsHandler.Summary)
		admin.GET("/metrics/daily", metricsHandler.Daily)

		// RAG 回归评测：评测集 CRUD + 无命中问题回流补录 + 评测轮触发与查询
		admin.GET("/rag-eval/cases", ragEvalHandler.ListCases)
		admin.POST("/rag-eval/cases", ragEvalHandler.CreateCase)
		admin.PUT("/rag-eval/cases/:id", ragEvalHandler.UpdateCase)
		admin.DELETE("/rag-eval/cases/:id", ragEvalHandler.DeleteCase)
		admin.POST("/rag-eval/cases/import-unanswered", ragEvalHandler.ImportFromUnanswered)
		admin.POST("/rag-eval/run", ragEvalHandler.RunEval)
		admin.GET("/rag-eval/runs", ragEvalHandler.ListRuns)
		admin.GET("/rag-eval/runs/:id", ragEvalHandler.GetRun)
	}

	// 前台只读（公开）
	{
		v1.GET("/portal/articles", portalHandler.ListArticles)
		v1.GET("/portal/articles/:slug", portalHandler.GetArticle)
		v1.GET("/portal/articles/:slug/related", portalHandler.RelatedArticles)
		v1.GET("/portal/figures/:file", portalHandler.Figure)
		// 唯一的前台写操作：点赞（按 IP 去重）
		v1.POST("/portal/articles/:slug/like", portalHandler.LikeArticle)
		// RAG 技术问答：SSE 流式接口，公开可访问（按 IP 限流）
		v1.POST("/portal/ask", ragHandler.Ask)
	}

	return r
}
