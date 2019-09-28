package tests

import (
	"fmt"
	"math/big"
	"os/exec"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/suite"

	"github.com/reserve-protocol/rsv-beta/abi"
	"github.com/reserve-protocol/rsv-beta/soltools"
)

func TestBasket(t *testing.T) {
	suite.Run(t, new(BasketSuite))
}

type BasketSuite struct {
	TestSuite
	weights []*big.Int
}

var (
	// Compile-time check that BasketSuite implements the interfaces we think it does.
	// If it does not implement these interfaces, then the corresponding setup and teardown
	// functions will not actually run.
	_ suite.BeforeTest       = &BasketSuite{}
	_ suite.SetupAllSuite    = &BasketSuite{}
	_ suite.TearDownAllSuite = &BasketSuite{}
)

// SetupSuite runs once, before all of the tests in the suite.
func (s *BasketSuite) SetupSuite() {
	s.setup()
}

// TearDownSuite runs once, after all of the tests in the suite.
func (s *BasketSuite) TearDownSuite() {
	if coverageEnabled {
		// Write coverage profile to disk.
		s.Assert().NoError(s.node.(*soltools.Backend).WriteCoverage())

		// Close the node.js process.
		s.Assert().NoError(s.node.(*soltools.Backend).Close())

		// Process coverage profile into an HTML report.
		if out, err := exec.Command("npx", "istanbul", "report", "html").CombinedOutput(); err != nil {
			fmt.Println()
			fmt.Println("I generated coverage information in coverage/coverage.json.")
			fmt.Println("I tried to process it with `istanbul` to turn it into a readable report, but failed.")
			fmt.Println("The error I got when running istanbul was:", err)
			fmt.Println("Istanbul's output was:\n" + string(out))
		}
	}
}

// BeforeTest runs before each test in the suite.
func (s *BasketSuite) BeforeTest(suiteName, testName string) {
	s.owner = s.account[0]

	// Deploy collateral ERC20s
	s.erc20s = make([]*abi.BasicERC20, 3)
	s.erc20Addresses = make([]common.Address, 3)
	for i := 0; i < 3; i++ {
		erc20Address, _, erc20, err := abi.DeployBasicERC20(s.signer, s.node)
		s.Require().NoError(err)
		s.erc20s[i] = erc20
		s.erc20Addresses[i] = erc20Address
	}

	s.weights = makeLinearWeights(bigInt(1), len(s.erc20s))

	// Make a simple basket
	basketAddress, tx, basket, err := abi.DeployBasket(
		s.signer,
		s.node,
		zeroAddress(),
		s.erc20Addresses,
		s.weights,
	)

	s.requireTxStrongly(tx, err)()
	s.basketAddress = basketAddress
	s.basket = basket
}

// TestState checks to make sure state is set up correctly after construction.
func (s *BasketSuite) TestState() {
	// Check that all variables in state are set correctly.
	for i, address := range s.erc20Addresses {
		foundAddress, err := s.basket.Tokens(nil, bigInt(uint32(i)))
		s.Require().NoError(err)
		s.Equal(address, foundAddress)

		foundWeight, err := s.basket.Weights(nil, address)
		s.Require().NoError(err)
		s.Equal(s.weights[i].String(), foundWeight.String())

		foundHas, err := s.basket.Has(nil, address)
		s.Require().NoError(err)
		s.Equal(true, foundHas)
	}
}

// TestGetters checks to make sure the view functions work as expected.
func (s *BasketSuite) TestViews() {
	// `getTokens` function.
	tokens, err := s.basket.GetTokens(nil)
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(s.erc20Addresses, tokens))

	// `size` function.
	size, err := s.basket.Size(nil)
	s.Require().NoError(err)
	s.Equal(bigInt(uint32(len(s.erc20Addresses))).String(), size.String())
}

// TestSuccessiveBasketWithEmptyParams tries deploying a second basket from a different account.
// This basket has no tokens, so should carry over tokens from the first basket.
func (s *BasketSuite) TestSuccessiveBasketWithEmptyParams() {
	deployer := s.account[1]

	var emptyTokens []common.Address
	var emptyWeights []*big.Int
	// Deploy a new basket from a different account, but based off the first basket.
	_, tx, basket, err := abi.DeployBasket(
		signer(deployer),
		s.node,
		s.basketAddress,
		emptyTokens,
		emptyWeights,
	)

	s.requireTxStrongly(tx, err)()

	// Our two baskets should be identical in every way.
	for i, _ := range s.erc20Addresses {
		// State
		firstToken, err := s.basket.Tokens(nil, bigInt(uint32(i)))
		s.Require().NoError(err)
		secondToken, err := basket.Tokens(nil, bigInt(uint32(i)))
		s.Require().NoError(err)
		s.Equal(firstToken, secondToken)

		firstWeight, err := s.basket.Weights(nil, firstToken)
		s.Require().NoError(err)
		secondWeight, err := basket.Weights(nil, firstToken)
		s.Require().NoError(err)
		s.Equal(firstWeight.String(), secondWeight.String())

		firstHas, err := s.basket.Has(nil, firstToken)
		s.Require().NoError(err)
		secondHas, err := basket.Has(nil, firstToken)
		s.Require().NoError(err)
		s.Equal(firstHas, secondHas)
	}

	// `getTokens()`
	firstTokens, err := s.basket.GetTokens(nil)
	s.Require().NoError(err)
	secondTokens, err := basket.GetTokens(nil)
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(firstTokens, secondTokens))

	// `size()`
	firstSize, err := s.basket.Size(nil)
	s.Require().NoError(err)
	secondSize, err := basket.Size(nil)
	s.Equal(firstSize, secondSize)
}

// TestSuccessiveBasketWithAdditionalParams deploys a 2nd basket with a new token.
func (s *BasketSuite) TestSuccessiveBasketWithAdditionalParams() {
	deployer := s.account[1]
	newToken := s.account[2].address()
	newWeight := bigInt(uint32(9))

	moreTokens := []common.Address{newToken}
	moreWeights := []*big.Int{newWeight}
	// Deploy a new basket from a different account, but based off the first basket.
	_, tx, basket, err := abi.DeployBasket(
		signer(deployer),
		s.node,
		s.basketAddress,
		moreTokens,
		moreWeights,
	)

	s.requireTxStrongly(tx, err)()

	// The second basket should be bigger.
	firstSize, err := s.basket.Size(nil)
	s.Require().NoError(err)
	secondSize, err := basket.Size(nil)
	s.Equal(bigInt(0).Add(firstSize, bigInt(1)), secondSize)

	// The token lists should differ by 1 token address.
	firstTokens, err := s.basket.GetTokens(nil)
	s.Require().NoError(err)
	secondTokens, err := basket.GetTokens(nil)
	s.Require().NoError(err)
	var expectedTokens []common.Address
	expectedTokens = append(expectedTokens, newToken)
	for _, tok := range firstTokens {
		expectedTokens = append(expectedTokens, tok)
	}
	s.True(reflect.DeepEqual(expectedTokens, secondTokens))

	// The new token should have the right weight.
	weight, err := basket.Weights(nil, newToken)
	s.Require().NoError(err)
	s.Equal(newWeight.String(), weight.String())

	// After that, our two baskets should be identical in every way for the erc20Addresses.
	for i, _ := range s.erc20Addresses {
		// State
		firstToken, err := s.basket.Tokens(nil, bigInt(uint32(i)))
		s.Require().NoError(err)
		secondToken, err := basket.Tokens(nil, bigInt(uint32(i+1)))
		s.Require().NoError(err)
		s.Equal(firstToken, secondToken)

		firstWeight, err := s.basket.Weights(nil, firstToken)
		s.Require().NoError(err)
		secondWeight, err := basket.Weights(nil, firstToken)
		s.Require().NoError(err)
		s.Equal(firstWeight.String(), secondWeight.String())

		firstHas, err := s.basket.Has(nil, firstToken)
		s.Require().NoError(err)
		secondHas, err := basket.Has(nil, firstToken)
		s.Require().NoError(err)
		s.Equal(firstHas, secondHas)
	}
}

// TestNegativeCases checks to make sure invalid basket constructions revert.
func (s *BasketSuite) TestNegativeCases() {
	// Case 1: Tokens is longer than Weights.
	deployer := s.account[1]
	tokens := s.erc20Addresses
	var weights []*big.Int
	_, tx, _, err := abi.DeployBasket(
		signer(deployer),
		s.node,
		s.basketAddress,
		tokens,
		weights,
	)
	s.requireTxFails(tx, err)

	// Case 2: Weights is longer than Tokens.
	tokens = []common.Address{}
	weights = []*big.Int{bigInt(1)}
	_, tx, _, err = abi.DeployBasket(
		signer(deployer),
		s.node,
		s.basketAddress,
		tokens,
		weights,
	)
	s.requireTxFails(tx, err)

	// Case 3: Basket is too big.
	var longTokens []common.Address
	var longWeights []*big.Int
	for i := 0; i < 101; i++ {
		longTokens = append(longTokens, s.account[2].address())
		longWeights = append(longWeights, bigInt(1))
	}
	_, tx, _, err = abi.DeployBasket(
		signer(deployer),
		s.node,
		s.basketAddress,
		longTokens,
		longWeights,
	)
	s.requireTxFails(tx, err)

	// Case 4: PrevBasket is not actually a basket.
	tokens = []common.Address{s.account[2].address()}
	weights = []*big.Int{bigInt(1)}
	_, tx, _, err = abi.DeployBasket(
		signer(deployer),
		s.node,
		s.account[3].address(),
		tokens,
		weights,
	)
	s.requireTxFails(tx, err)
}
