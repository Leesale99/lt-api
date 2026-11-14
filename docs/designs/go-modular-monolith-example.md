# Go Modular Monolith Example

This document provides a complete, runnable-style blueprint for a Go **Modular Monolith** that combines **Layered Architecture** for simpler CRUD modules (`catalog`) and **Hexagonal (Ports & Adapters) Architecture** for complex bounded contexts (`orders`).

## 1. Directory Structure

```text
shop-monolith/
├── cmd/
│   └── api/
│       └── main.go                     # Wires modules, shared DB, event bus, and HTTP router
├── modules/
│   ├── catalog/                        # Simple CRUD -> Layered architecture
│   │   ├── catalog.go                  # Public module facade, DTOs, and route registration
│   │   └── internal/                   # Compiler-enforced private implementation
│   │       ├── model/
│   │       │   └── product.go
│   │       ├── repository/
│   │       │   └── product_repo.go
│   │       ├── service/
│   │       │   └── product_service.go
│   │       └── handler/
│   │           └── product_handler.go
│   └── orders/                         # Complex domain -> Hexagonal architecture
│       ├── orders.go                   # Public module facade, events, and route registration
│       └── internal/                   # Compiler-enforced private implementation
│           ├── core/
│           │   ├── domain/
│           │   │   └── order.go
│           │   ├── port/
│           │   │   └── order_port.go
│           │   └── service/
│           │       └── order_service.go
│           └── adapter/
│               ├── inbound/
│               │   └── http/
│               │       └── order_handler.go
│               └── outbound/
│                   ├── memory/
│                   │   └── order_repo.go
│                   └── catalog_acl.go  # Anti-Corruption Layer calling catalog facade
├── shared/
│   └── events/
│       └── bus.go                      # In-memory event bus interface + implementation
└── go.mod
```

---

## 2. Core Principles

- **Compile-Time Boundaries**: Go's `internal/` package rule prevents any package outside `modules/<name>/` from importing `modules/<name>/internal/...`.
- **Public Module Facade**: Each module exposes a single root file (`catalog.go`, `orders.go`) with its public interface, DTOs, and route registration.
- **Schema Isolation**: Modules share a database connection pool in the monolith, but each module only queries its own schema (e.g., `catalog.*` vs. `orders.*`).
- **Async-First Cross-Module Communication**: Modules publish and subscribe to domain events via `shared/events.Bus`, making future microservice extraction straightforward.

---

## 3. Shared Infrastructure

### `go.mod`
Defines the module path for the monolith.

```go
module shop-monolith

go 1.23
```

### `shared/events/bus.go`
Provides a lightweight, in-memory event bus so modules can communicate without direct coupling. Later, this interface can be backed by NATS or Kafka.

```go
package events

import (
	"context"
	"sync"
)

type Event interface {
	Topic() string
}

type Handler func(ctx context.Context, event Event) error

type Bus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(topic string, handler Handler)
}

type InMemoryBus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{handlers: make(map[string][]Handler)}
}

func (b *InMemoryBus) Subscribe(topic string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], handler)
}

func (b *InMemoryBus) Publish(ctx context.Context, event Event) error {
	b.mu.RLock()
	handlers := b.handlers[event.Topic()]
	b.mu.RUnlock()

	for _, h := range handlers {
		if err := h(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
```

---

## 4. `catalog` Module (Layered Architecture)

### `modules/catalog/catalog.go`
The public entry point for the `catalog` module. It exposes the `CatalogAPI` interface and `ProductDTO` for other modules, wires internal layers, and registers HTTP routes.

```go
package catalog

import (
	"context"
	"net/http"

	"shop-monolith/modules/catalog/internal/handler"
	"shop-monolith/modules/catalog/internal/repository"
	"shop-monolith/modules/catalog/internal/service"
)

type ProductDTO struct {
	ID    string
	Name  string
	Price float64
}

type CatalogAPI interface {
	GetProduct(ctx context.Context, id string) (ProductDTO, error)
}

type Module struct {
	svc *service.ProductService
	h   *handler.ProductHandler
}

func NewModule() *Module {
	repo := repository.NewInMemoryProductRepo()
	svc := service.NewProductService(repo)
	h := handler.NewProductHandler(svc)
	return &Module{svc: svc, h: h}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /catalog/products/{id}", m.h.Get)
}

func (m *Module) GetProduct(ctx context.Context, id string) (ProductDTO, error) {
	p, err := m.svc.GetProduct(ctx, id)
	if err != nil {
		return ProductDTO{}, err
	}
	return ProductDTO{ID: p.ID, Name: p.Name, Price: p.Price}, nil
}
```

### `modules/catalog/internal/model/product.go`
Defines the internal domain model and errors for `catalog`.

```go
package model

import "errors"

var ErrProductNotFound = errors.New("product not found")

type Product struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}
```

### `modules/catalog/internal/repository/product_repo.go`
Handles data access for products.

```go
package repository

import (
	"context"
	"sync"

	"shop-monolith/modules/catalog/internal/model"
)

type ProductRepository interface {
	FindByID(ctx context.Context, id string) (model.Product, error)
}

type InMemoryProductRepo struct {
	mu       sync.RWMutex
	products map[string]model.Product
}

func NewInMemoryProductRepo() *InMemoryProductRepo {
	return &InMemoryProductRepo{
		products: map[string]model.Product{
			"p1": {ID: "p1", Name: "Mechanical Keyboard", Price: 120.00},
		},
	}
}

func (r *InMemoryProductRepo) FindByID(_ context.Context, id string) (model.Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.products[id]
	if !ok {
		return model.Product{}, model.ErrProductNotFound
	}
	return p, nil
}
```

### `modules/catalog/internal/service/product_service.go`
Contains business logic for `catalog`.

```go
package service

import (
	"context"

	"shop-monolith/modules/catalog/internal/model"
	"shop-monolith/modules/catalog/internal/repository"
)

type ProductService struct {
	repo repository.ProductRepository
}

func NewProductService(repo repository.ProductRepository) *ProductService {
	return &ProductService{repo: repo}
}

func (s *ProductService) GetProduct(ctx context.Context, id string) (model.Product, error) {
	return s.repo.FindByID(ctx, id)
}
```

### `modules/catalog/internal/handler/product_handler.go`
Handles HTTP requests for `catalog`.

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"shop-monolith/modules/catalog/internal/model"
	"shop-monolith/modules/catalog/internal/service"
)

type ProductHandler struct {
	svc *service.ProductService
}

func NewProductHandler(svc *service.ProductService) *ProductHandler {
	return &ProductHandler{svc: svc}
}

func (h *ProductHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	product, err := h.svc.GetProduct(r.Context(), id)
	if errors.Is(err, model.ErrProductNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(product)
}
```

---

## 5. `orders` Module (Hexagonal Architecture)

### `modules/orders/orders.go`
The public facade for `orders`. It wires the core service with its inbound/outbound adapters and registers routes.

```go
package orders

import (
	"net/http"

	"shop-monolith/modules/catalog"
	httpadapter "shop-monolith/modules/orders/internal/adapter/inbound/http"
	"shop-monolith/modules/orders/internal/adapter/outbound"
	"shop-monolith/modules/orders/internal/adapter/outbound/memory"
	"shop-monolith/modules/orders/internal/core/service"
	"shop-monolith/shared/events"
)

type Module struct {
	h *httpadapter.OrderHandler
}

func NewModule(cat catalog.CatalogAPI, bus events.Bus) *Module {
	repo := memory.NewOrderRepo()
	catACL := outbound.NewCatalogACL(cat)
	svc := service.NewOrderService(repo, catACL, bus)
	h := httpadapter.NewOrderHandler(svc)
	return &Module{h: h}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /orders", m.h.Create)
	mux.HandleFunc("GET /orders/{id}", m.h.Get)
}
```

### `modules/orders/internal/core/domain/order.go`
Pure domain entities, domain events, and errors with zero external dependencies.

```go
package domain

import "errors"

var ErrOrderNotFound = errors.New("order not found")

type Order struct {
	ID        string  `json:"id"`
	ProductID string  `json:"product_id"`
	Amount    float64 `json:"amount"`
}

type OrderPlacedEvent struct {
	OrderID   string
	ProductID string
}

func (e OrderPlacedEvent) Topic() string {
	return "orders.placed"
}
```

### `modules/orders/internal/core/port/order_port.go`
Defines inbound (driving) and outbound (driven) ports owned by the core.

```go
package port

import (
	"context"

	"shop-monolith/modules/orders/internal/core/domain"
)

type OrderService interface {
	CreateOrder(ctx context.Context, order domain.Order) error
	GetOrder(ctx context.Context, id string) (domain.Order, error)
}

type OrderRepository interface {
	Save(ctx context.Context, order domain.Order) error
	FindByID(ctx context.Context, id string) (domain.Order, error)
}

type CatalogPort interface {
	GetProductPrice(ctx context.Context, productID string) (float64, error)
}
```

### `modules/orders/internal/core/service/order_service.go`
Implements the `OrderService` inbound port, orchestrating validation, pricing via `CatalogPort`, persistence via `OrderRepository`, and event publishing.

```go
package service

import (
	"context"
	"errors"

	"shop-monolith/modules/orders/internal/core/domain"
	"shop-monolith/modules/orders/internal/core/port"
	"shop-monolith/shared/events"
)

type orderService struct {
	repo    port.OrderRepository
	catalog port.CatalogPort
	bus     events.Bus
}

func NewOrderService(repo port.OrderRepository, catalog port.CatalogPort, bus events.Bus) port.OrderService {
	return &orderService{repo: repo, catalog: catalog, bus: bus}
}

func (s *orderService) CreateOrder(ctx context.Context, order domain.Order) error {
	price, err := s.catalog.GetProductPrice(ctx, order.ProductID)
	if err != nil {
		return err
	}
	if price <= 0 {
		return errors.New("invalid product price")
	}
	order.Amount = price

	if err := s.repo.Save(ctx, order); err != nil {
		return err
	}
	return s.bus.Publish(ctx, domain.OrderPlacedEvent{
		OrderID:   order.ID,
		ProductID: order.ProductID,
	})
}

func (s *orderService) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	return s.repo.FindByID(ctx, id)
}
```

### `modules/orders/internal/adapter/outbound/catalog_acl.go`
Anti-Corruption Layer (ACL) adapter that translates `catalog.CatalogAPI` DTOs into `orders` domain primitives.

```go
package outbound

import (
	"context"

	"shop-monolith/modules/catalog"
	"shop-monolith/modules/orders/internal/core/port"
)

type CatalogACL struct {
	api catalog.CatalogAPI
}

func NewCatalogACL(api catalog.CatalogAPI) port.CatalogPort {
	return &CatalogACL{api: api}
}

func (a *CatalogACL) GetProductPrice(ctx context.Context, productID string) (float64, error) {
	p, err := a.api.GetProduct(ctx, productID)
	if err != nil {
		return 0, err
	}
	return p.Price, nil
}
```

### `modules/orders/internal/adapter/outbound/memory/order_repo.go`
In-memory implementation of `port.OrderRepository`.

```go
package memory

import (
	"context"
	"sync"

	"shop-monolith/modules/orders/internal/core/domain"
	"shop-monolith/modules/orders/internal/core/port"
)

type OrderRepo struct {
	mu     sync.RWMutex
	orders map[string]domain.Order
}

func NewOrderRepo() port.OrderRepository {
	return &OrderRepo{orders: make(map[string]domain.Order)}
}

func (r *OrderRepo) Save(_ context.Context, order domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[order.ID] = order
	return nil
}

func (r *OrderRepo) FindByID(_ context.Context, id string) (domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	order, ok := r.orders[id]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return order, nil
}
```

### `modules/orders/internal/adapter/inbound/http/order_handler.go`
HTTP driving adapter that calls `port.OrderService`.

```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"shop-monolith/modules/orders/internal/core/domain"
	"shop-monolith/modules/orders/internal/core/port"
)

type OrderHandler struct {
	svc port.OrderService
}

func NewOrderHandler(svc port.OrderService) *OrderHandler {
	return &OrderHandler{svc: svc}
}

func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var order domain.Order
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.svc.CreateOrder(r.Context(), order); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *OrderHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, err := h.svc.GetOrder(r.Context(), id)
	if errors.Is(err, domain.ErrOrderNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}
```

---

## 6. Application Entry Point

### `cmd/api/main.go`
Wires the shared event bus, initializes both modules, registers their HTTP routes, and starts the server.

```go
package main

import (
	"log"
	"net/http"

	"shop-monolith/modules/catalog"
	"shop-monolith/modules/orders"
	"shop-monolith/shared/events"
)

func main() {
	bus := events.NewInMemoryBus()

	catalogMod := catalog.NewModule()
	ordersMod := orders.NewModule(catalogMod, bus)

	mux := http.NewServeMux()
	catalogMod.RegisterRoutes(mux)
	ordersMod.RegisterRoutes(mux)

	log.Println("shop-monolith listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
```
