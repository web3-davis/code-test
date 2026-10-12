// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/access/AccessControl.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

/// @title BTCTransfer
/// @notice 只记录 BTC 转账请求和状态，不持有 BTC、不接收 ETH、不直接操作 Bitcoin 网络。
///         Completed 依赖授权后台（OPERATOR_ROLE）的可信通知，合约无法独立验证 BTC 链状态。
contract BTCTransfer is AccessControl, Pausable, ReentrancyGuard {
    bytes32 public constant OPERATOR_ROLE = keccak256("OPERATOR_ROLE");

    enum Status {
        Pending,
        Processing,
        Completed,
        Failed
    }

    struct TransferRequest {
        uint256 requestId;
        address requester;
        string btcAddress;
        uint64 amount; // satoshi
        bytes32 btcTxHash; // BTC txid（大端显示顺序的 32 字节）
        Status status;
    }

    uint256 public nextRequestId = 1; // 自增保证唯一，0 表示不存在
    mapping(uint256 => TransferRequest) private requests;

    event BTCTransferRequested(uint256 indexed requestId, address indexed requester, string btcAddress, uint64 amount);
    event BTCTransferProcessing(uint256 indexed requestId);
    event BTCTransferCompleted(uint256 indexed requestId, bytes32 btcTxHash, uint64 amount, string btcAddress);
    event BTCTransferFailed(uint256 indexed requestId, string reason);

    constructor(address admin, address operator) {
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(OPERATOR_ROLE, operator);
        // OPERATOR_ROLE 的 admin 默认是 DEFAULT_ADMIN_ROLE：只有管理员能 grantRole / revokeRole
    }

    modifier exists(uint256 requestId) {
        require(requests[requestId].requester != address(0), "request not found");
        _;
    }

    /// @notice 用户申请 BTC 转账。只做基本参数检查，地址完整校验交给 Go 后台。
    function requestBTCTransfer(string calldata btcAddress, uint64 amount)
        external
        whenNotPaused
        nonReentrant
        returns (uint256 requestId)
    {
        require(bytes(btcAddress).length > 0, "empty btc address");
        require(amount > 0, "amount must be > 0");

        requestId = nextRequestId++;
        requests[requestId] = TransferRequest({
            requestId: requestId,
            requester: msg.sender,
            btcAddress: btcAddress,
            amount: amount,
            btcTxHash: bytes32(0),
            status: Status.Pending
        });

        emit BTCTransferRequested(requestId, msg.sender, btcAddress, amount);
    }

    /// @notice 后台开始处理：Pending -> Processing
    function startBTCTransfer(uint256 requestId)
        external
        onlyRole(OPERATOR_ROLE)
        whenNotPaused
        nonReentrant
        exists(requestId)
    {
        TransferRequest storage r = requests[requestId];
        require(r.status == Status.Pending, "not pending");
        r.status = Status.Processing;
        emit BTCTransferProcessing(requestId);
    }

    /// @notice BTC 交易达到确认数后由后台通知完成：Processing -> Completed
    /// @dev 不加 whenNotPaused：BTC 已经转出，暂停期间也应允许如实记录结果
    function completeBTCTransfer(uint256 requestId, bytes32 btcTxHash)
        external
        onlyRole(OPERATOR_ROLE)
        nonReentrant
        exists(requestId)
    {
        TransferRequest storage r = requests[requestId];
        require(r.status == Status.Processing, "not processing"); // 已 Completed 不能重复完成
        require(btcTxHash != bytes32(0), "empty btc tx hash");

        r.btcTxHash = btcTxHash;
        r.status = Status.Completed;
        emit BTCTransferCompleted(requestId, btcTxHash, r.amount, r.btcAddress);
    }

    /// @notice 标记失败：Pending/Processing -> Failed
    /// @dev 后台只能在“确定 BTC 未广播”时调用；已广播未确认的交易必须保持 Processing，
    ///      否则可能被重新发起导致重复转账。Completed 不允许变为 Failed。
    function failBTCTransfer(uint256 requestId, string calldata reason)
        external
        onlyRole(OPERATOR_ROLE)
        nonReentrant
        exists(requestId)
    {
        TransferRequest storage r = requests[requestId];
        require(r.status == Status.Pending || r.status == Status.Processing, "invalid status");
        r.status = Status.Failed;
        emit BTCTransferFailed(requestId, reason);
    }

    function getRequest(uint256 requestId) external view exists(requestId) returns (TransferRequest memory) {
        return requests[requestId];
    }

    function pause() external onlyRole(DEFAULT_ADMIN_ROLE) {
        _pause();
    }

    function unpause() external onlyRole(DEFAULT_ADMIN_ROLE) {
        _unpause();
    }
}
