/*
 * Copyright (c) 2018. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */

// Package grpc provides the gRPC service to communicate with the Pydio's user persistence layer.
package grpc

import (
	"context"
	"encoding/base64"
	"os"
	"strings"

	"github.com/micro/go-micro"
	"github.com/micro/go-micro/errors"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/config"
	"github.com/pydio/cells/common/log"
	"github.com/pydio/cells/common/proto/idm"
	"github.com/pydio/cells/common/service"
	"github.com/pydio/cells/common/service/context"
	service2 "github.com/pydio/cells/common/service/proto"
	"github.com/pydio/cells/common/utils"
	"github.com/pydio/cells/idm/user"
)

const (
	ENV_PYDIO_ADMIN_USER_LOGIN    = "PYDIO_ADMIN_USER_LOGIN"
	ENV_PYDIO_ADMIN_USER_PASSWORD = "PYDIO_ADMIN_USER_PASSWORD"
)

func init() {
	service.NewService(
		service.Name(common.SERVICE_GRPC_NAMESPACE_+common.SERVICE_USER),
		service.Tag(common.SERVICE_TAG_IDM),
		service.Description("Users persistence layer"),
		service.Migrations([]*service.Migration{
			{
				TargetVersion: service.FirstRun(),
				Up:            InitDefaults,
			},
		}),
		service.WithStorage(user.NewDAO, "idm_user"),
		service.WithMicro(func(m micro.Service) error {
			idm.RegisterUserServiceHandler(m.Options().Server, new(Handler))

			return nil
		}),
	)
}

func InitDefaults(ctx context.Context) error {

	var login, pwd string
	dao := servicecontext.GetDAO(ctx).(user.DAO)

	if os.Getenv(ENV_PYDIO_ADMIN_USER_LOGIN) != "" && os.Getenv(ENV_PYDIO_ADMIN_USER_PASSWORD) != "" {
		login = os.Getenv(ENV_PYDIO_ADMIN_USER_LOGIN)
		pwd = os.Getenv(ENV_PYDIO_ADMIN_USER_PASSWORD)
	}

	if rootConfig := config.Get("defaults", "root").String(""); rootConfig != "" {
		sDec, _ := base64.StdEncoding.DecodeString(rootConfig)
		parts := strings.Split(string(sDec), "||||")
		login = parts[0]
		pwd = parts[1]
		// Now remove from configs
		config.Del("defaults", "root")
		utils.SaveConfigs()
	}

	if login != "" && pwd != "" {
		log.Logger(ctx).Info("Initialization: creating admin user: " + login)
		// Check if user exists
		newUser, err := CreateIfNotExists(ctx, dao, &idm.User{
			Login:      login,
			Password:   pwd,
			Attributes: map[string]string{"profile": common.PYDIO_PROFILE_ADMIN},
		})
		if err != nil {
			return err
		} else if newUser != nil {
			builder := service2.NewResourcePoliciesBuilder()
			builder = builder.WithProfileRead(common.PYDIO_PROFILE_STANDARD)
			builder = builder.WithUserWrite(login)
			builder = builder.WithProfileWrite(common.PYDIO_PROFILE_ADMIN)
			if err2 := dao.AddPolicies(false, newUser.Uuid, builder.Policies()); err2 != nil {
				return err2
			}
		}
	}

	log.Logger(ctx).Info("Initialization: creating s3 anonymous user")

	newAnon, err := CreateIfNotExists(ctx, dao, &idm.User{
		Login:      common.PYDIO_S3ANON_USERNAME,
		Password:   common.PYDIO_S3ANON_USERNAME,
		Attributes: map[string]string{"profile": common.PYDIO_S3ANON_PROFILE},
	})
	if err != nil {
		return err
	}

	if newAnon != nil {
		builder := service2.NewResourcePoliciesBuilder()
		builder = builder.WithUserRead(common.PYDIO_S3ANON_USERNAME)
		builder = builder.WithProfileRead(common.PYDIO_PROFILE_ADMIN)
		builder = builder.WithProfileWrite(common.PYDIO_PROFILE_ADMIN)
		if err2 := dao.AddPolicies(false, newAnon.Uuid, builder.Policies()); err2 != nil {
			return err2
		}
	}

	return nil
}

// CreateIfNotExists creates a user if DAO.Bind() call returns a 404 error.
func CreateIfNotExists(ctx context.Context, dao user.DAO, user *idm.User) (*idm.User, error) {
	if _, err := dao.Bind(user.Login, user.Password); err != nil && errors.Parse(err.Error()).Code != 404 {
		return nil, err
	} else if err == nil {
		log.Logger(ctx).Info("Skipping user " + user.Login + ", already exists")
		return nil, nil
	}
	// User is not created yet, add it now
	out, _, err := dao.Add(user)
	if err != nil {
		return nil, err
	}
	return out.(*idm.User), nil
}