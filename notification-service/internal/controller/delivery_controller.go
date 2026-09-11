package controller

import (
	"net/http"
	"strconv"

	"realtime-chat-platform/notification-service/internal/repository"

	"github.com/labstack/echo/v4"
)

const (
	defaultDeliveryLimit = 20
	maxDeliveryLimit     = 100
)

type DeliveryController struct {
	deliveryRepo repository.DeliveryRepositoryInterface
}

func NewDeliveryController(deliveryRepo repository.DeliveryRepositoryInterface) *DeliveryController {
	return &DeliveryController{deliveryRepo: deliveryRepo}
}

// List handles GET /deliveries?limit= - the caller's own delivery log, newest
// first. It reads the repository directly: there is no business rule between
// the row and the caller.
func (dc *DeliveryController) List(c echo.Context) error {
	userID, ok := userIDFrom(c)
	if !ok {
		return unauthorized(c)
	}

	limit := defaultDeliveryLimit
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid limit parameter"})
		}
		limit = min(parsed, maxDeliveryLimit)
	}

	deliveries, err := dc.deliveryRepo.ListByUserID(userID, limit)
	if err != nil {
		return respondError(c, err, "failed to list deliveries")
	}

	return c.JSON(http.StatusOK, deliveries)
}
