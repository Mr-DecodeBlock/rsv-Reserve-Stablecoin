// +build regular

package tests

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/suite"

	"github.com/reserve-protocol/rsv-beta/abi"
)

func TestManager(t *testing.T) {
	suite.Run(t, new(ManagerSuite))
}

type ManagerSuite struct {
	TestSuite
}

var (
	// Compile-time check that ManagerSuite implements the interfaces we think it does.
	// If it does not implement these interfaces, then the corresponding setup and teardown
	// functions will not actually run.
	_ suite.BeforeTest       = &ManagerSuite{}
	_ suite.SetupAllSuite    = &ManagerSuite{}
	_ suite.TearDownAllSuite = &ManagerSuite{}
)

// SetupSuite runs once, before all of the tests in the suite.
func (s *ManagerSuite) SetupSuite() {
	s.setup()
}

// BeforeTest runs before each test in the suite.
func (s *ManagerSuite) BeforeTest(suiteName, testName string) {
	s.owner = s.account[0]
	s.operator = s.account[1]
	s.proposer = s.account[5]

	// Deploy Reserve and store a handle to the Go binding and the contract address.
	reserveAddress, tx, reserve, err := abi.DeployReserve(s.signer, s.node)

	s.logParsers = map[common.Address]logParser{
		reserveAddress: reserve,
	}

	s.requireTx(tx, err)
	s.reserve = reserve
	s.reserveAddress = reserveAddress

	// Unpause Reserve.
	s.requireTxWithStrictEvents(s.reserve.Unpause(s.signer))(
		abi.ReserveUnpaused{Account: s.owner.address()},
	)

	// Get the Go binding and contract address for the new ReserveEternalStorage contract.
	s.eternalStorageAddress, err = s.reserve.GetEternalStorageAddress(nil)
	s.Require().NoError(err)
	s.eternalStorage, err = abi.NewReserveEternalStorage(s.eternalStorageAddress, s.node)
	s.Require().NoError(err)

	s.logParsers[s.eternalStorageAddress] = s.eternalStorage

	// Accept ownership of eternal storage.
	s.requireTxWithStrictEvents(s.eternalStorage.AcceptOwnership(s.signer))(
		abi.ReserveEternalStorageOwnershipTransferred{
			PreviousOwner: s.reserveAddress, NewOwner: s.account[0].address(),
		},
	)

	// Vault.
	vaultAddress, tx, vault, err := abi.DeployVault(s.signer, s.node)

	s.logParsers[vaultAddress] = vault
	s.requireTxWithStrictEvents(tx, err)(
		abi.VaultOwnershipTransferred{
			PreviousOwner: zeroAddress(), NewOwner: s.owner.address(),
		},
		abi.VaultManagerTransferred{
			PreviousManager: zeroAddress(), NewManager: s.owner.address(),
		},
	)
	s.vault = vault
	s.vaultAddress = vaultAddress

	// ProposalFactory.
	propFactoryAddress, tx, propFactory, err := abi.DeployProposalFactory(s.signer, s.node)
	s.logParsers[propFactoryAddress] = propFactory
	s.requireTx(tx, err)

	s.proposalFactory = propFactory
	s.proposalFactoryAddress = propFactoryAddress

	// Deploy collateral ERC20s.
	s.erc20s = make([]*abi.BasicERC20, 3)
	s.erc20Addresses = make([]common.Address, 3)
	for i := 0; i < 3; i++ {
		erc20Address, _, erc20, err := abi.DeployBasicERC20(s.signer, s.node)
		s.Require().NoError(err)

		s.erc20s[i] = erc20
		s.erc20Addresses[i] = erc20Address
		s.logParsers[erc20Address] = erc20
	}

	// Basket.
	s.weights = []*big.Int{shiftLeft(1, 36), shiftLeft(2, 36), shiftLeft(3, 36)}

	// Make a simple basket
	basketAddress, tx, basket, err := abi.DeployBasket(
		s.signer, s.node, zeroAddress(), s.erc20Addresses, s.weights,
	)
	s.requireTxWithStrictEvents(tx, err)
	s.NotEqual(zeroAddress(), basketAddress)
	s.basketAddress, s.basket = basketAddress, basket

	// Manager.
	managerAddress, tx, manager, err := abi.DeployManager(
		s.signer, s.node,
		vaultAddress, reserveAddress, propFactoryAddress, basketAddress, s.operator.address(), bigInt(0),
	)

	s.logParsers[managerAddress] = manager
	s.requireTx(tx, err)(abi.ManagerOwnershipTransferred{
		PreviousOwner: zeroAddress(), NewOwner: s.owner.address(),
	})
	s.manager = manager
	s.managerAddress = managerAddress

	// Confirm we start in emergency state.
	emergency, err := s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(true, emergency)

	// Unpause from emergency.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, false))(
		abi.ManagerEmergencyChanged{OldVal: true, NewVal: false},
	)

	// Confirm we are unpaused from emergency.
	emergency, err = s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(false, emergency)

	// Set all auths to Manager.
	s.requireTxWithStrictEvents(s.reserve.ChangeMinter(s.signer, managerAddress))(
		abi.ReserveMinterChanged{NewMinter: managerAddress},
	)
	s.requireTxWithStrictEvents(s.reserve.ChangePauser(s.signer, managerAddress))(
		abi.ReservePauserChanged{NewPauser: managerAddress},
	)
	s.requireTxWithStrictEvents(s.vault.ChangeManager(s.signer, managerAddress))(
		abi.VaultManagerTransferred{PreviousManager: s.owner.address(), NewManager: managerAddress},
	)

	// Fund and set allowances.
	amounts := []*big.Int{shiftLeft(1, 46), shiftLeft(1, 46), shiftLeft(1, 46)}
	s.fundAccountWithErc20sAndApprove(s.proposer, amounts)

	// Pass a WeightProposal so we are able to Issue/Redeem.
	s.weights = []*big.Int{shiftLeft(1, 35), shiftLeft(3, 35), shiftLeft(6, 35)}
	s.changeBasketUsingWeightProposal(s.erc20Addresses, s.weights)
}

func (s *ManagerSuite) TestDeploy() {}

// TestConstructor tests that the constructor sets initial state appropriately.
func (s *ManagerSuite) TestConstructor() {
	vaultAddr, err := s.manager.TrustedVault(nil)
	s.Require().NoError(err)
	s.Equal(s.vaultAddress, vaultAddr)

	rsvAddr, err := s.manager.TrustedRSV(nil)
	s.Require().NoError(err)
	s.Equal(s.reserveAddress, rsvAddr)

	proposalFactory, err := s.manager.TrustedProposalFactory(nil)
	s.Require().NoError(err)
	s.Equal(s.proposalFactoryAddress, proposalFactory)

	operator, err := s.manager.Operator(nil)
	s.Require().NoError(err)
	s.Equal(s.operator.address(), operator)

	seigniorage, err := s.manager.Seigniorage(nil)
	s.Require().NoError(err)
	s.Equal(bigInt(0).String(), seigniorage.String())

	// `emergency` is tested in `BeforeTest`
}

// TestSetIssuancePaused tests that `setIssuancePaused` changes the state as expected.
func (s *ManagerSuite) TestSetIssuancePaused() {
	// Confirm Issuance is Unpaused.
	paused, err := s.manager.IssuancePaused(nil)
	s.Require().NoError(err)
	s.Equal(false, paused)

	// Pause.
	s.requireTxWithStrictEvents(s.manager.SetIssuancePaused(s.signer, true))(
		abi.ManagerIssuancePausedChanged{OldVal: false, NewVal: true},
	)

	// Confirm Issuance is Paused.
	paused, err = s.manager.IssuancePaused(nil)
	s.Require().NoError(err)
	s.Equal(true, paused)

	// Unpause.
	s.requireTxWithStrictEvents(s.manager.SetIssuancePaused(s.signer, false))(
		abi.ManagerIssuancePausedChanged{OldVal: true, NewVal: false},
	)

	// Confirm Issuance is Unpaused.
	paused, err = s.manager.IssuancePaused(nil)
	s.Require().NoError(err)
	s.Equal(false, paused)
}

// TestSetIssuancePausedIsProtected tests that `setIssuancePaused` can only be called by owner.
func (s *ManagerSuite) TestSetIssuancePausedIsProtected() {
	s.requireTxFails(s.manager.SetIssuancePaused(signer(s.account[2]), true))
	s.requireTxFails(s.manager.SetIssuancePaused(signer(s.operator), true))
}

// TestSetEmergency tests that `setEmergency` changes the state as expected.
func (s *ManagerSuite) TestSetEmergency() {
	// Confirm we being not in an emergency.
	emergency, err := s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(false, emergency)

	// Pause for emergency.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, true))(
		abi.ManagerEmergencyChanged{OldVal: false, NewVal: true},
	)

	// Confirm we are in an emergency.
	emergency, err = s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(true, emergency)

	// Unpause for emergency.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, false))(
		abi.ManagerEmergencyChanged{OldVal: true, NewVal: false},
	)

	// Confirm we are not in an emergency.
	emergency, err = s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(false, emergency)
}

// TestSetEmergencyIsProtected tests that `setEmergency` can only be called by owner.
func (s *ManagerSuite) TestSetEmergencyIsProtected() {
	s.requireTxFails(s.manager.SetEmergency(signer(s.account[2]), true))
	s.requireTxFails(s.manager.SetEmergency(signer(s.operator), true))
}

// TestSetOperator tests that `setOperator` manipulates state correctly.
func (s *ManagerSuite) TestSetOperator() {
	newOperator := s.account[5].address()
	s.requireTxWithStrictEvents(s.manager.SetOperator(s.signer, newOperator))(
		abi.ManagerOperatorChanged{
			OldAccount: s.operator.address(), NewAccount: newOperator,
		},
	)

	// Check that state is correct.
	foundOperator, err := s.manager.Operator(nil)
	s.Require().NoError(err)
	s.Equal(newOperator, foundOperator)
}

// TestSetOperatorIsProtected tests that `setOperator` can only be called by owner.
func (s *ManagerSuite) TestSetOperatorIsProtected() {
	s.requireTxFails(s.manager.SetOperator(signer(s.account[2]), s.account[5].address()))
	s.requireTxFails(s.manager.SetOperator(signer(s.operator), s.account[5].address()))
}

// TestSetSeigniorage tests that `setSeigniorage` manipulates state correctly.
func (s *ManagerSuite) TestSetSeigniorage() {
	seigniorage := bigInt(1)
	s.requireTxWithStrictEvents(s.manager.SetSeigniorage(s.signer, seigniorage))(
		abi.ManagerSeigniorageChanged{
			OldVal: bigInt(0), NewVal: seigniorage,
		},
	)

	// Check that state is correct.
	foundSeigniorage, err := s.manager.Seigniorage(nil)
	s.Require().NoError(err)
	s.Equal(seigniorage.String(), foundSeigniorage.String())
}

// TestSetSeigniorageIsProtected tests that `setSeigniorage` can only be called by owner.
func (s *ManagerSuite) TestSetSeigniorageIsProtected() {
	seigniorage := bigInt(1)
	s.requireTxFails(s.manager.SetSeigniorage(signer(s.account[2]), seigniorage))
	s.requireTxFails(s.manager.SetSeigniorage(signer(s.operator), seigniorage))
}

// TestSetSeigniorageRequires tests that `setSeigniorage` require statements works as expected
func (s *ManagerSuite) TestSetSeigniorageRequires() {
	seigniorage := bigInt(1001)
	s.requireTxFails(s.manager.SetSeigniorage(s.signer, seigniorage))
}

// TestSetDelay tests that `setDelay` manipulates state correctly.
func (s *ManagerSuite) TestSetDelay() {
	delay := bigInt(172800) // 48 hours
	s.requireTxWithStrictEvents(s.manager.SetDelay(s.signer, delay))(
		abi.ManagerDelayChanged{
			OldVal: bigInt(86400), NewVal: delay,
		},
	)

	// Check that state is correct.
	foundDelay, err := s.manager.Delay(nil)
	s.Require().NoError(err)
	s.Equal(delay.String(), foundDelay.String())
}

// TestSetDelayIsProtected tests that `setDelay` can only be called by owner.
func (s *ManagerSuite) TestSetDelayIsProtected() {
	delay := bigInt(1)
	s.requireTxFails(s.manager.SetDelay(signer(s.account[2]), delay))
	s.requireTxFails(s.manager.SetDelay(signer(s.operator), delay))
}

// TestClearProposals tests that `clearProposals` manipulates state correctly.
func (s *ManagerSuite) TestClearProposals() {
	// ProposalsLength should start at 1.
	proposalsLength, err := s.manager.ProposalsLength(nil)
	s.Require().NoError(err)
	s.Equal(bigInt(1).String(), proposalsLength.String())

	// Clear it.
	s.requireTxWithStrictEvents(s.manager.ClearProposals(s.signer))(
		abi.ManagerProposalsCleared{},
	)

	// Check that the length is now 0.
	proposalsLength, err = s.manager.ProposalsLength(nil)
	s.Require().NoError(err)
	s.Equal(bigInt(0).String(), proposalsLength.String())
}

// TestClearProposalsIsProtected tests that `clearProposals` can only be called by owner.
func (s *ManagerSuite) TestClearProposalsIsProtected() {
	s.requireTxFails(s.manager.ClearProposals(signer(s.account[2])))
	s.requireTxFails(s.manager.ClearProposals(signer(s.operator)))
}

// TestIssue tests that `issue` costs the correct amounts given basket + seigniorage.
func (s *ManagerSuite) TestIssue() {
	buyer := s.account[4]

	//First set seigniorage, in BPS
	seigniorage := bigInt(10) // 0.1%
	s.requireTxWithStrictEvents(s.manager.SetSeigniorage(s.signer, seigniorage))(
		abi.ManagerSeigniorageChanged{
			OldVal: bigInt(0), NewVal: seigniorage,
		},
	)

	rsvAmount := shiftLeft(1, 27) // 1 billion
	expectedAmounts := s.computeExpectedIssueAmounts(seigniorage, rsvAmount)
	s.fundAccountWithErc20sAndApprove(buyer, expectedAmounts)

	// Issue.
	s.requireTx(s.manager.Issue(signer(buyer), rsvAmount))

	// Expect RSV balance.
	balance, err := s.reserve.BalanceOf(nil, buyer.address())
	s.Require().NoError(err)
	s.Equal(rsvAmount.String(), balance.String())

	for i, erc20 := range s.erc20s {
		// Expect no leftover tokens.
		balance, err = erc20.BalanceOf(nil, buyer.address())
		s.Require().NoError(err)
		s.Equal(bigInt(0).String(), balance.String())

		// Expect tokens are all in the vault.
		balance, err = erc20.BalanceOf(nil, s.vaultAddress)
		s.Require().NoError(err)
		s.Equal(expectedAmounts[i].String(), balance.String())
	}

	s.assertManagerCollateralized()
}

// TestIssueIsProtected tests that `issue` reverts when in an emergency or it is paused.
func (s *ManagerSuite) TestIssueIsProtected() {
	amount := bigInt(1)

	// We should be able to issue initially.
	s.requireTx(s.manager.Issue(signer(s.proposer), amount))

	// Set `emergency` to true.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, true))(
		abi.ManagerEmergencyChanged{OldVal: false, NewVal: true},
	)

	// Confirm `emergency` is true.
	emergency, err := s.manager.Emergency(nil)
	s.Require().NoError(err)
	s.Equal(true, emergency)

	// Issue should fail.
	s.requireTxFails(s.manager.Issue(signer(s.proposer), amount))

	// Set `emergency` to false.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, false))(
		abi.ManagerEmergencyChanged{OldVal: true, NewVal: false},
	)

	// Now we should be able to issue.
	s.requireTx(s.manager.Issue(signer(s.proposer), amount))

	// Pause just issuance.
	s.requireTxWithStrictEvents(s.manager.SetIssuancePaused(s.signer, true))(
		abi.ManagerIssuancePausedChanged{OldVal: false, NewVal: true},
	)

	// Confirm we are Paused.
	paused, err := s.manager.IssuancePaused(nil)
	s.Require().NoError(err)
	s.Equal(true, paused)

	// Issue should fail now.
	s.requireTxFails(s.manager.Issue(signer(s.proposer), amount))

	// Unpause issuance.
	s.requireTxWithStrictEvents(s.manager.SetIssuancePaused(s.signer, false))(
		abi.ManagerIssuancePausedChanged{OldVal: true, NewVal: false},
	)

	// Now we should be able to issue.
	s.requireTx(s.manager.Issue(signer(s.proposer), amount))

}

// TestIssueRequireStatements tests that `issue` reverts when Paused.
func (s *ManagerSuite) TestIssueRequireStatements() {
	amount := bigInt(1)

	// Issue should succeed first.
	s.requireTx(s.manager.Issue(signer(s.proposer), amount))
	s.assertManagerCollateralized()

	// Issue should fail now.
	s.requireTxFails(s.manager.Issue(signer(s.proposer), bigInt(0)))
	s.assertManagerCollateralized()
}

// TestRedeem tests that `redeem` compensates the person with the correct amounts.
func (s *ManagerSuite) TestRedeem() {
	// Issue.
	rsvAmount := shiftLeft(1, 27) // 1 billion
	s.requireTx(s.manager.Issue(signer(s.proposer), rsvAmount))

	redeemer := s.account[4]

	// Send the RSV to someone else who doesn't have any Erc20s.
	s.requireTx(s.reserve.Transfer(signer(s.proposer), redeemer.address(), rsvAmount))

	// Redeem that RSV.
	s.requireTx(s.reserve.Approve(signer(redeemer), s.managerAddress, rsvAmount))
	s.requireTx(s.manager.Redeem(signer(redeemer), rsvAmount))

	// Figure out what to expect back.
	amounts := s.computeExpectedRedeemAmounts(rsvAmount)

	// Assert our balances are what we expected.
	for i, erc20 := range s.erc20s {
		// Expect no leftover tokens.
		balance, err := erc20.BalanceOf(nil, redeemer.address())
		s.Require().NoError(err)
		s.Equal(amounts[i].String(), balance.String())
	}

	s.assertManagerCollateralized()
}

// TestRedeemIsProtected tests that `redeem` compensates the person with the correct amounts.
func (s *ManagerSuite) TestRedeemIsProtected() {
	// Issue.
	rsvAmount := shiftLeft(1, 27) // 1 billion
	s.requireTx(s.manager.Issue(signer(s.proposer), rsvAmount))

	// Make sure we have the balance we expect to have.
	rsvBalance, err := s.reserve.BalanceOf(nil, s.proposer.address())
	s.Require().NoError(err)
	s.Equal(rsvAmount.String(), rsvBalance.String())

	// Approve the manager to spend our RSV.
	s.requireTx(s.reserve.Approve(signer(s.proposer), s.managerAddress, rsvAmount))(
		abi.ReserveApproval{
			Owner:   s.proposer.address(),
			Spender: s.managerAddress,
			Value:   rsvAmount,
		},
	)

	// Redeem a tiny amount first to make sure it works.
	s.requireTx(s.manager.Redeem(signer(s.proposer), bigInt(1)))

	// Emergency Pause.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, true))(
		abi.ManagerEmergencyChanged{OldVal: false, NewVal: true},
	)

	// Confirm the same redemption now fails.
	s.requireTxFails(s.manager.Redeem(signer(s.proposer), bigInt(1)))

	// Unpause from emergency.
	s.requireTxWithStrictEvents(s.manager.SetEmergency(s.signer, false))(
		abi.ManagerEmergencyChanged{OldVal: true, NewVal: false},
	)

	// Should be able to Redeem.
	s.requireTx(s.manager.Redeem(signer(s.proposer), bigInt(1)))
}

// TestRedeemRequireStatements tests that `redeem` reverts for 0 RSV.
func (s *ManagerSuite) TestRedeemRequireStatements() {
	// Issue.
	rsvAmount := shiftLeft(1, 27) // 1 billion
	s.requireTx(s.manager.Issue(signer(s.proposer), rsvAmount))

	// Make sure we have the balance we expect to have.
	rsvBalance, err := s.reserve.BalanceOf(nil, s.proposer.address())
	s.Require().NoError(err)
	s.Equal(rsvAmount.String(), rsvBalance.String())

	// Approve the manager to spend our RSV.
	s.requireTx(s.reserve.Approve(signer(s.proposer), s.managerAddress, rsvAmount))(
		abi.ReserveApproval{
			Owner:   s.proposer.address(),
			Spender: s.managerAddress,
			Value:   rsvAmount,
		},
	)

	// Redeem a tiny amount first to make sure it works.
	s.requireTx(s.manager.Redeem(signer(s.proposer), bigInt(1)))

	// Confirm redeeming for 0 fails.
	s.requireTxFails(s.manager.Redeem(signer(s.proposer), bigInt(0)))

	s.assertManagerCollateralized()
}

// TestProposeWeightsUseCase sets a basket, issues RSV, changes the basket, and redeems RSV.
func (s *ManagerSuite) TestProposeWeightsFullUsecase() {
	// Issue a billion RSV.
	rsvToIssue := shiftLeft(1, 27) // 1 billion
	s.requireTx(s.manager.Issue(signer(s.proposer), rsvToIssue))
	s.assertManagerCollateralized()

	// Change to a new basket.
	newWeights := []*big.Int{shiftLeft(6, 35), shiftLeft(1, 35), shiftLeft(3, 35)}
	s.changeBasketUsingWeightProposal(s.erc20Addresses, newWeights)

	// Approve the manager to spend a billion RSV.
	s.requireTx(s.reserve.Approve(signer(s.proposer), s.managerAddress, rsvToIssue))(
		abi.ReserveApproval{Owner: s.proposer.address(), Spender: s.managerAddress, Value: rsvToIssue},
	)

	// Redeem a billion RSV.
	s.requireTx(s.manager.Redeem(signer(s.proposer), rsvToIssue))
	s.assertManagerCollateralized()

	// We should be back to zero RSV supply.
	s.assertRSVTotalSupply(bigInt(0))

}

// TestProposeSwapFullUsecase sets up a basket with a WeightProposal, issues RSV,
// changes the basket using a SwapProposal, and redeems the RSV.
func (s *ManagerSuite) TestProposeSwapFullUsecase() {
	// Issue a billion RSV.
	rsvToIssue := shiftLeft(1, 27) // 1 billion
	s.requireTx(s.manager.Issue(signer(s.proposer), rsvToIssue))
	s.assertManagerCollateralized()

	// Change to a new basket using a SwapProposal
	amounts := []*big.Int{shiftLeft(2, 17), shiftLeft(3, 17), shiftLeft(1, 17)}
	toVault := []bool{true, false, true}
	s.changeBasketUsingSwapProposal(s.erc20Addresses, amounts, toVault)

	// Approve the manager to spend a billion RSV.
	s.requireTx(s.reserve.Approve(signer(s.proposer), s.managerAddress, rsvToIssue))(
		abi.ReserveApproval{Owner: s.proposer.address(), Spender: s.managerAddress, Value: rsvToIssue},
	)

	// Redeem a billion RSV.
	s.requireTx(s.manager.Redeem(signer(s.proposer), rsvToIssue))
	s.assertManagerCollateralized()

	// We should be back to zero RSV supply.
	s.assertRSVTotalSupply(bigInt(0))
}
