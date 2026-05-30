// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "github.com/gin-gonic/gin"

type Handlers interface {
	SetupRoutes(v1 *gin.RouterGroup)
}
