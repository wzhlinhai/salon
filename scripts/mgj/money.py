"""金额、数量和积分的十进制读取与格式化。"""

from decimal import Decimal, InvalidOperation, ROUND_HALF_UP

CENT = Decimal("0.01")


def decimal_from(value):
    """把数据库里的文本或数字表示读成 Decimal，不经过二进制浮点运算。"""
    if value is None:
        raise InvalidOperation("empty")
    if isinstance(value, Decimal):
        return value
    if isinstance(value, bytes):
        value = value.decode("utf-8")
    text = str(value).strip()
    if text == "":
        raise InvalidOperation("empty")
    return Decimal(text)


def quantize(value):
    """按四舍五入（ROUND_HALF_UP）量化到分。"""
    return decimal_from(value).quantize(CENT, rounding=ROUND_HALF_UP)


def money(value):
    """返回固定两位小数的金额文本。"""
    amount = quantize(value)
    if amount == 0:
        amount = Decimal("0.00")
    return format(amount, "f")


def money_or_zero(value):
    """读取金额；空值或无法解析时按零处理。"""
    try:
        return quantize(value)
    except (InvalidOperation, ValueError):
        return Decimal("0.00")


def raw_decimal_or_none(value):
    """读取未量化的金额；空值或无法解析时返回 None。"""
    try:
        return decimal_from(value)
    except (InvalidOperation, ValueError):
        return None


def median(values):
    """返回一组 Decimal 的中位数；空列表返回 None。"""
    ordered = sorted(values)
    if not ordered:
        return None
    middle = len(ordered) // 2
    if len(ordered) % 2:
        return ordered[middle]
    return (ordered[middle - 1] + ordered[middle]) / 2
