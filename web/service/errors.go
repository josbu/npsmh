package service

import (
	"errors"

	"github.com/djylb/nps/lib/file"
)

var (
	ErrClientQRCodeTextRequired    = errors.New("missing qr code text")
	ErrClientModifyFailed          = errors.New("client modify failed")
	ErrClientLimitExceeded         = errors.New("client limit exceeded")
	ErrClientVKeyDuplicate         = errors.New("client verify key duplicate")
	ErrClientRateLimitExceeded     = errors.New("client rate limit exceeds owner quota")
	ErrClientConnLimitExceeded     = errors.New("client connection limit exceeds owner quota")
	ErrWebUsernameDuplicate        = errors.New("client web username duplicate")
	ErrInvalidRegistration         = errors.New("invalid registration")
	ErrReservedUsername            = errors.New("reserved username")
	ErrUserNotFound                = errors.New("user not found")
	ErrUserUsernameRequired        = errors.New("user username is required")
	ErrUserPasswordRequired        = errors.New("user password is required")
	ErrInvalidTOTPSecret           = errors.New("invalid totp secret")
	ErrUserHasClients              = errors.New("user still has attached clients")
	ErrClientNotFound              = errors.New("client not found")
	ErrTunnelNotFound              = errors.New("tunnel not found")
	ErrHostNotFound                = errors.New("host not found")
	ErrHostExists                  = errors.New("host already exists")
	ErrModeRequired                = errors.New("mode is required")
	ErrPortUnavailable             = errors.New("port unavailable")
	ErrTunnelLimitExceeded         = errors.New("tunnel limit exceeded")
	ErrHostLimitExceeded           = errors.New("host limit exceeded")
	ErrClientResourceLimitExceeded = errors.New("client resource limit exceeded")
	ErrClientIdentifierRequired    = errors.New("id or vkey is required")
	ErrClientIdentifierConflict    = errors.New("client id and verify key target mismatch")
	ErrStoreNotInitialized         = errors.New("store is not initialized")
	ErrInvalidTrafficItems         = errors.New("invalid traffic items")
	ErrTrafficItemsEmpty           = errors.New("traffic items are empty")
	ErrTrafficClientRequired       = errors.New("items or client_id/vkey is required")
	ErrTrafficTargetRequired       = errors.New("client_id or vkey is required")
	ErrSnapshotExportUnsupported   = errors.New("snapshot export is not supported")
	ErrSnapshotImportUnsupported   = errors.New("snapshot import is not supported")
	ErrManagementPlatformNotFound  = errors.New("management platform not found")
	ErrInvalidCallbackQueueAction  = errors.New("invalid callback queue action")
	ErrInvalidCredentials          = errors.New("invalid credentials")
	ErrUnauthenticated             = errors.New("unauthenticated")
	ErrForbidden                   = errors.New("forbidden")
	ErrRevisionConflict            = errors.New("resource revision conflict")
	ErrStandaloneTokenInvalid      = errors.New("invalid standalone token")
	ErrStandaloneTokenExpired      = errors.New("standalone token expired")
	ErrStandaloneTokenUnavailable  = errors.New("standalone token is unavailable")
)

func mapClientServiceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, file.ErrClientNotFound) {
		return ErrClientNotFound
	}
	if errors.Is(err, file.ErrRevisionConflict) {
		return ErrRevisionConflict
	}
	return err
}

func mapUserServiceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, file.ErrUserNotFound) {
		return ErrUserNotFound
	}
	if errors.Is(err, file.ErrRevisionConflict) {
		return ErrRevisionConflict
	}
	return err
}
