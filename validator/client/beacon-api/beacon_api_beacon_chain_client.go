package beacon_api

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/v4/beacon-chain/rpc/apimiddleware"
	"github.com/prysmaticlabs/prysm/v4/consensus-types/primitives"
	ethpb "github.com/prysmaticlabs/prysm/v4/proto/prysm/v1alpha1"
	"github.com/prysmaticlabs/prysm/v4/time/slots"
	"github.com/prysmaticlabs/prysm/v4/validator/client/iface"
)

type beaconApiBeaconChainClient struct {
	fallbackClient          iface.BeaconChainClient
	jsonRestHandler         jsonRestHandler
	stateValidatorsProvider stateValidatorsProvider
}

func (c beaconApiBeaconChainClient) GetChainHead(ctx context.Context, in *empty.Empty) (*ethpb.ChainHead, error) {
	if c.fallbackClient != nil {
		return c.fallbackClient.GetChainHead(ctx, in)
	}

	// TODO: Implement me
	panic("beaconApiBeaconChainClient.GetChainHead is not implemented. To use a fallback client, pass a fallback client as the last argument of NewBeaconApiBeaconChainClientWithFallback.")
}

func (c beaconApiBeaconChainClient) ListValidatorBalances(ctx context.Context, in *ethpb.ListValidatorBalancesRequest) (*ethpb.ValidatorBalances, error) {
	if c.fallbackClient != nil {
		return c.fallbackClient.ListValidatorBalances(ctx, in)
	}

	// TODO: Implement me
	panic("beaconApiBeaconChainClient.ListValidatorBalances is not implemented. To use a fallback client, pass a fallback client as the last argument of NewBeaconApiBeaconChainClientWithFallback.")
}

func (c beaconApiBeaconChainClient) ListValidators(ctx context.Context, in *ethpb.ListValidatorsRequest) (*ethpb.Validators, error) {
	pageSize := in.PageSize

	// We follow the gRPC behavior here, which returns a maximum of 250 results when pageSize == 0
	if pageSize == 0 {
		pageSize = 250
	}

	var pageToken uint64
	var err error

	if in.PageToken != "" {
		if pageToken, err = strconv.ParseUint(in.PageToken, 10, 64); err != nil {
			return nil, errors.Wrapf(err, "failed to parse page token `%s`", in.PageToken)
		}
	}

	var statuses []string
	if in.Active {
		statuses = []string{"active"}
	}

	pubkeys := make([]string, len(in.PublicKeys))
	for idx, pubkey := range in.PublicKeys {
		pubkeys[idx] = hexutil.Encode(pubkey)
	}

	var stateValidators *apimiddleware.StateValidatorsResponseJson
	var epoch primitives.Epoch

	switch queryFilter := in.QueryFilter.(type) {
	case *ethpb.ListValidatorsRequest_Epoch:
		slot, err := slots.EpochStart(queryFilter.Epoch)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get first slot for epoch `%d`", queryFilter.Epoch)
		}
		if stateValidators, err = c.stateValidatorsProvider.GetStateValidatorsForSlot(ctx, slot, pubkeys, in.Indices, statuses); err != nil {
			return nil, errors.Wrapf(err, "failed to get state validators for slot `%d`", slot)
		}
		epoch = slots.ToEpoch(slot)
	case *ethpb.ListValidatorsRequest_Genesis:
		if stateValidators, err = c.stateValidatorsProvider.GetStateValidatorsForSlot(ctx, 0, pubkeys, in.Indices, statuses); err != nil {
			return nil, errors.Wrapf(err, "failed to get genesis state validators")
		}
		epoch = 0
	case nil:
		if stateValidators, err = c.stateValidatorsProvider.GetStateValidatorsForHead(ctx, pubkeys, in.Indices, statuses); err != nil {
			return nil, errors.Wrap(err, "failed to get head state validators")
		}

		blockHeader := apimiddleware.BlockHeaderResponseJson{}
		if _, err := c.jsonRestHandler.GetRestJsonResponse(ctx, "/eth/v1/beacon/headers/head", &blockHeader); err != nil {
			return nil, errors.Wrap(err, "failed to get head block header")
		}

		if blockHeader.Data == nil || blockHeader.Data.Header == nil {
			return nil, errors.New("block header data is nil")
		}

		if blockHeader.Data.Header.Message == nil {
			return nil, errors.New("block header message is nil")
		}

		slot, err := strconv.ParseUint(blockHeader.Data.Header.Message.Slot, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse header slot `%s`", blockHeader.Data.Header.Message.Slot)
		}

		epoch = slots.ToEpoch(primitives.Slot(slot))
	default:
		return nil, errors.Errorf("unsupported query filter type `%v`", reflect.TypeOf(queryFilter))
	}

	if stateValidators.Data == nil {
		return nil, errors.New("state validators data is nil")
	}

	start := pageToken * uint64(pageSize)
	if start > uint64(len(stateValidators.Data)) {
		start = uint64(len(stateValidators.Data))
	}

	end := start + uint64(pageSize)
	if end > uint64(len(stateValidators.Data)) {
		end = uint64(len(stateValidators.Data))
	}

	validators := make([]*ethpb.Validators_ValidatorContainer, end-start)
	for idx := start; idx < end; idx++ {
		stateValidator := stateValidators.Data[idx]

		if stateValidator.Validator == nil {
			return nil, errors.Errorf("state validator at index `%d` is nil", idx)
		}

		pubkey, err := hexutil.Decode(stateValidator.Validator.PublicKey)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to decode validator pubkey `%s`", stateValidator.Validator.PublicKey)
		}

		withdrawalCredentials, err := hexutil.Decode(stateValidator.Validator.WithdrawalCredentials)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to decode validator withdrawal credentials `%s`", stateValidator.Validator.WithdrawalCredentials)
		}

		effectiveBalance, err := strconv.ParseUint(stateValidator.Validator.EffectiveBalance, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator effective balance `%s`", stateValidator.Validator.EffectiveBalance)
		}

		validatorIndex, err := strconv.ParseUint(stateValidator.Index, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator index `%s`", stateValidator.Index)
		}

		activationEligibilityEpoch, err := strconv.ParseUint(stateValidator.Validator.ActivationEligibilityEpoch, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator activation eligibility epoch `%s`", stateValidator.Validator.ActivationEligibilityEpoch)
		}

		activationEpoch, err := strconv.ParseUint(stateValidator.Validator.ActivationEpoch, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator activation epoch `%s`", stateValidator.Validator.ActivationEpoch)
		}

		exitEpoch, err := strconv.ParseUint(stateValidator.Validator.ExitEpoch, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator exit epoch `%s`", stateValidator.Validator.ExitEpoch)
		}

		withdrawableEpoch, err := strconv.ParseUint(stateValidator.Validator.WithdrawableEpoch, 10, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse validator withdrawable epoch `%s`", stateValidator.Validator.WithdrawableEpoch)
		}

		validators[idx-start] = &ethpb.Validators_ValidatorContainer{
			Index: primitives.ValidatorIndex(validatorIndex),
			Validator: &ethpb.Validator{
				PublicKey:                  pubkey,
				WithdrawalCredentials:      withdrawalCredentials,
				EffectiveBalance:           effectiveBalance,
				Slashed:                    stateValidator.Validator.Slashed,
				ActivationEligibilityEpoch: primitives.Epoch(activationEligibilityEpoch),
				ActivationEpoch:            primitives.Epoch(activationEpoch),
				ExitEpoch:                  primitives.Epoch(exitEpoch),
				WithdrawableEpoch:          primitives.Epoch(withdrawableEpoch),
			},
		}
	}

	var nextPageToken string
	if end < uint64(len(stateValidators.Data)) {
		nextPageToken = strconv.FormatUint(pageToken+1, 10)
	}

	return &ethpb.Validators{
		TotalSize:     int32(len(stateValidators.Data)),
		Epoch:         epoch,
		ValidatorList: validators,
		NextPageToken: nextPageToken,
	}, nil
}

func (c beaconApiBeaconChainClient) GetValidatorQueue(ctx context.Context, in *empty.Empty) (*ethpb.ValidatorQueue, error) {
	if c.fallbackClient != nil {
		return c.fallbackClient.GetValidatorQueue(ctx, in)
	}

	// TODO: Implement me
	panic("beaconApiBeaconChainClient.GetValidatorQueue is not implemented. To use a fallback client, pass a fallback client as the last argument of NewBeaconApiBeaconChainClientWithFallback.")
}

func (c beaconApiBeaconChainClient) GetValidatorPerformance(ctx context.Context, in *ethpb.ValidatorPerformanceRequest) (*ethpb.ValidatorPerformanceResponse, error) {
	if c.fallbackClient != nil {
		return c.fallbackClient.GetValidatorPerformance(ctx, in)
	}

	// TODO: Implement me
	panic("beaconApiBeaconChainClient.GetValidatorPerformance is not implemented. To use a fallback client, pass a fallback client as the last argument of NewBeaconApiBeaconChainClientWithFallback.")
}

func (c beaconApiBeaconChainClient) GetValidatorParticipation(ctx context.Context, in *ethpb.GetValidatorParticipationRequest) (*ethpb.ValidatorParticipationResponse, error) {
	if c.fallbackClient != nil {
		return c.fallbackClient.GetValidatorParticipation(ctx, in)
	}

	// TODO: Implement me
	panic("beaconApiBeaconChainClient.GetValidatorParticipation is not implemented. To use a fallback client, pass a fallback client as the last argument of NewBeaconApiBeaconChainClientWithFallback.")
}

func NewBeaconApiBeaconChainClientWithFallback(host string, timeout time.Duration, fallbackClient iface.BeaconChainClient) iface.BeaconChainClient {
	jsonRestHandler := beaconApiJsonRestHandler{
		httpClient: http.Client{Timeout: timeout},
		host:       host,
	}

	return &beaconApiBeaconChainClient{
		jsonRestHandler:         jsonRestHandler,
		fallbackClient:          fallbackClient,
		stateValidatorsProvider: beaconApiStateValidatorsProvider{jsonRestHandler: jsonRestHandler},
	}
}
