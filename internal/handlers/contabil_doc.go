package handlers

// ── Contabil Proxy — Swagger Documentation ─────────────────────────
// These functions exist solely for swagger documentation.
// All contabil proxy routes forward to the external Contabil API via ContabilProxy.

import "net/http"

// ── Clients ────────────────────────────────────────────────────────

// contabilListClients godoc
// @Summary Listar clientes contábeis
// @Description Proxy: lista todos os clientes do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/clients [get]
func contabilListClients(_ http.ResponseWriter, _ *http.Request) {}

// contabilGetClient godoc
// @Summary Obter cliente contábil
// @Description Proxy: retorna um cliente específico pelo ID
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Client ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/clients/{id} [get]
func contabilGetClient(_ http.ResponseWriter, _ *http.Request) {}

// contabilCreateClient godoc
// @Summary Criar cliente contábil
// @Description Proxy: cria um novo cliente no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados do cliente"
// @Success 201 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/clients [post]
func contabilCreateClient(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateClient godoc
// @Summary Atualizar cliente contábil
// @Description Proxy: atualiza um cliente existente no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Client ID"
// @Param body body object true "Dados para atualização"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/clients/{id} [put]
func contabilUpdateClient(_ http.ResponseWriter, _ *http.Request) {}

// contabilDeleteClient godoc
// @Summary Remover cliente contábil
// @Description Proxy: remove um cliente do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Client ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/clients/{id} [delete]
func contabilDeleteClient(_ http.ResponseWriter, _ *http.Request) {}

// ── Bills ──────────────────────────────────────────────────────────

// contabilListBills godoc
// @Summary Listar cobranças
// @Description Proxy: lista todas as cobranças do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills [get]
func contabilListBills(_ http.ResponseWriter, _ *http.Request) {}

// contabilGetBill godoc
// @Summary Obter cobrança
// @Description Proxy: retorna uma cobrança específica pelo ID
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Bill ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills/{id} [get]
func contabilGetBill(_ http.ResponseWriter, _ *http.Request) {}

// contabilGenerateBills godoc
// @Summary Gerar cobranças
// @Description Proxy: gera cobranças para os clientes do sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Parâmetros de geração"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills/generate [post]
func contabilGenerateBills(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateBill godoc
// @Summary Atualizar cobrança
// @Description Proxy: atualiza uma cobrança existente
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Bill ID"
// @Param body body object true "Dados para atualização"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills/{id} [put]
func contabilUpdateBill(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateBillStatus godoc
// @Summary Atualizar status da cobrança
// @Description Proxy: atualiza o status de uma cobrança
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Bill ID"
// @Param body body object true "Novo status"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills/{id}/status [put]
func contabilUpdateBillStatus(_ http.ResponseWriter, _ *http.Request) {}

// contabilMarkBillPaid godoc
// @Summary Marcar cobrança como paga
// @Description Proxy: marca uma cobrança como paga no sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Bill ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/bills/{id}/paid [patch]
func contabilMarkBillPaid(_ http.ResponseWriter, _ *http.Request) {}

// ── Services ───────────────────────────────────────────────────────

// contabilListServices godoc
// @Summary Listar serviços contábeis
// @Description Proxy: lista todos os serviços cadastrados no sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/services [get]
func contabilListServices(_ http.ResponseWriter, _ *http.Request) {}

// contabilCreateService godoc
// @Summary Criar serviço contábil
// @Description Proxy: cria um novo serviço no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados do serviço"
// @Success 201 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/services [post]
func contabilCreateService(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateService godoc
// @Summary Atualizar serviço contábil
// @Description Proxy: atualiza um serviço existente
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Service ID"
// @Param body body object true "Dados para atualização"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/services/{id} [put]
func contabilUpdateService(_ http.ResponseWriter, _ *http.Request) {}

// contabilDeleteService godoc
// @Summary Remover serviço contábil
// @Description Proxy: remove um serviço do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Service ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/services/{id} [delete]
func contabilDeleteService(_ http.ResponseWriter, _ *http.Request) {}

// ── Dashboard ──────────────────────────────────────────────────────

// contabilDashboardSummary godoc
// @Summary Resumo do dashboard contábil
// @Description Proxy: retorna o resumo financeiro do dashboard contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/dashboard/summary [get]
func contabilDashboardSummary(_ http.ResponseWriter, _ *http.Request) {}

// contabilDashboardRevenue godoc
// @Summary Receita do dashboard contábil
// @Description Proxy: retorna dados de receita do dashboard contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/dashboard/revenue [get]
func contabilDashboardRevenue(_ http.ResponseWriter, _ *http.Request) {}

// ── Import ─────────────────────────────────────────────────────────

// contabilImportClientsPreview godoc
// @Summary Preview de importação de clientes
// @Description Proxy: faz preview da importação de clientes a partir de arquivo
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados do arquivo para preview"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/import/clients/preview [post]
func contabilImportClientsPreview(_ http.ResponseWriter, _ *http.Request) {}

// contabilImportClients godoc
// @Summary Importar clientes
// @Description Proxy: importa clientes a partir de arquivo para o sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados de importação"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/import/clients [post]
func contabilImportClients(_ http.ResponseWriter, _ *http.Request) {}

// contabilImportServicesPreview godoc
// @Summary Preview de importação de serviços
// @Description Proxy: faz preview da importação de serviços a partir de arquivo
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados do arquivo para preview"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/import/services/preview [post]
func contabilImportServicesPreview(_ http.ResponseWriter, _ *http.Request) {}

// contabilImportServices godoc
// @Summary Importar serviços
// @Description Proxy: importa serviços a partir de arquivo para o sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados de importação"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/import/services [post]
func contabilImportServices(_ http.ResponseWriter, _ *http.Request) {}

// ── Organizations ──────────────────────────────────────────────────

// contabilListOrganizations godoc
// @Summary Listar organizações contábeis
// @Description Proxy: lista todas as organizações do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/organizations [get]
func contabilListOrganizations(_ http.ResponseWriter, _ *http.Request) {}

// contabilCreateOrganization godoc
// @Summary Criar organização contábil
// @Description Proxy: cria uma nova organização no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados da organização"
// @Success 201 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/organizations [post]
func contabilCreateOrganization(_ http.ResponseWriter, _ *http.Request) {}

// contabilGetOrganization godoc
// @Summary Obter organização contábil
// @Description Proxy: retorna uma organização específica pelo ID
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/organizations/{id} [get]
func contabilGetOrganization(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateOrganization godoc
// @Summary Atualizar organização contábil
// @Description Proxy: atualiza uma organização existente
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Param body body object true "Dados para atualização"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/organizations/{id} [put]
func contabilUpdateOrganization(_ http.ResponseWriter, _ *http.Request) {}

// contabilDeleteOrganization godoc
// @Summary Remover organização contábil
// @Description Proxy: remove uma organização do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/organizations/{id} [delete]
func contabilDeleteOrganization(_ http.ResponseWriter, _ *http.Request) {}

// ── Users ──────────────────────────────────────────────────────────

// contabilListUsers godoc
// @Summary Listar usuários contábeis
// @Description Proxy: lista todos os usuários do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/users [get]
func contabilListUsers(_ http.ResponseWriter, _ *http.Request) {}

// contabilCreateUser godoc
// @Summary Criar usuário contábil
// @Description Proxy: cria um novo usuário no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Dados do usuário"
// @Success 201 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/users [post]
func contabilCreateUser(_ http.ResponseWriter, _ *http.Request) {}

// contabilGetUser godoc
// @Summary Obter usuário contábil
// @Description Proxy: retorna um usuário específico pelo ID
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/users/{id} [get]
func contabilGetUser(_ http.ResponseWriter, _ *http.Request) {}

// contabilUpdateUser godoc
// @Summary Atualizar usuário contábil
// @Description Proxy: atualiza um usuário existente no sistema contábil
// @Tags contabil-proxy
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param body body object true "Dados para atualização"
// @Success 200 {object} object
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/users/{id} [put]
func contabilUpdateUser(_ http.ResponseWriter, _ *http.Request) {}

// contabilToggleUserActive godoc
// @Summary Ativar/desativar usuário contábil
// @Description Proxy: alterna o status ativo/inativo de um usuário contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/users/{id}/toggle-active [patch]
func contabilToggleUserActive(_ http.ResponseWriter, _ *http.Request) {}

// ── Audit Logs ─────────────────────────────────────────────────────

// contabilListAuditLogs godoc
// @Summary Listar logs de auditoria
// @Description Proxy: lista os logs de auditoria do sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/audit-logs [get]
func contabilListAuditLogs(_ http.ResponseWriter, _ *http.Request) {}

// ── Roles ──────────────────────────────────────────────────────────

// contabilListRoles godoc
// @Summary Listar papéis contábeis
// @Description Proxy: lista os papéis disponíveis no sistema contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/roles [get]
func contabilListRoles(_ http.ResponseWriter, _ *http.Request) {}

// contabilGetRolePermissions godoc
// @Summary Obter permissões de um papel
// @Description Proxy: retorna as permissões associadas a um papel contábil
// @Tags contabil-proxy
// @Produce json
// @Security BearerAuth
// @Param role path string true "Role name"
// @Success 200 {object} object
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Not found"
// @Failure 502 {string} string "Contabil service unavailable"
// @Router /admin/contabil/roles/{role}/permissions [get]
func contabilGetRolePermissions(_ http.ResponseWriter, _ *http.Request) {}
