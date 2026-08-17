# Broadcast Address Calculation

```
Broadcast = IP | ^SubnetMask
```

## 1. What is a Broadcast Address?

A broadcast address is the address used to send a packet to all devices in a particular subnet.

For example:

```
IP:        192.168.1.10/24
Network:   192.168.1.0
Broadcast: 192.168.1.255
```

**The important rule:** for a broadcast address, all host bits are set to `1`.

## 2. IPv4 Has 32 Bits

An IPv4 address contains 32 bits, divided into 4 bytes:

```
192.168.1.10

11000000.10101000.00000001.00001010
  8 bits    8 bits    8 bits    8 bits
```

## 3. What Does /24 Mean?

Consider `192.168.1.10/24`.

`/24` means:

- First 24 bits → Network portion
- Remaining 8 bits → Host portion

```
11000000.10101000.00000001.00001010
^^^^^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^^
      Network (24 bits)      Host (8 bits)
```

## 4. Subnet Mask

For `/24`, the subnet mask is `255.255.255.0`.

In binary:

```
11111111.11111111.11111111.00000000
^^^^^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^^
      Network bits            Host bits
```

The meaning of the subnet mask:

| Bit | Meaning     |
| --- | ----------- |
| `1` | Network bit |
| `0` | Host bit    |

## 5. What Does `^SubnetMask` Mean?

In the formula `Broadcast = IP | ^SubnetMask`, `^` is the **bitwise NOT** operator. It flips every bit:

```
0 → 1
1 → 0
```

For `/24`:

```
Subnet Mask:   11111111.11111111.11111111.00000000
Apply NOT:
^SubnetMask:   00000000.00000000.00000000.11111111
```

So `^SubnetMask = 0.0.0.255`.

Notice:

```
Subnet Mask:
11111111.11111111.11111111.00000000
^^^^^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^^
     Network bits              Host bits

^SubnetMask:
00000000.00000000.00000000.11111111
^^^^^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^^
     Network bits              Host bits
```

After applying NOT:

- Network bits become `0`
- Host bits become `1`

This is exactly what's needed for calculating the broadcast address.

## 6. What Does `|` Mean?

`|` is the **bitwise OR** operator.

The OR rules:

```
0 | 0 = 0
0 | 1 = 1
1 | 0 = 1
1 | 1 = 1
```

The two most important rules:

```
x | 0 = x
x | 1 = 1
```

This is why the formula works.

## 7. Calculate Broadcast Address

Suppose `IP = 192.168.1.10/24`.

**Step 1 — IP in binary**

```
192.168.1.10
11000000.10101000.00000001.00001010
```

**Step 2 — Subnet mask**

```
255.255.255.0
11111111.11111111.11111111.00000000
```

**Step 3 — Apply NOT**

```
^SubnetMask:
00000000.00000000.00000000.11111111
```

**Step 4 — Apply OR**

```
  IP:          11000000.10101000.00000001.00001010
  OR
  ^SubnetMask: 00000000.00000000.00000000.11111111
  -----------------------------------------------------
  Result:      11000000.10101000.00000001.11111111
```

Convert back to decimal: `192.168.1.255`

**Therefore: `Broadcast = 192.168.1.255`**

## 8. Why Does This Work?

The important part is understanding what happens to each type of bit.

**Network bits**

For network bits: `^SubnetMask = 0`

Therefore: `IP bit | 0 = IP bit` → network bits remain unchanged.

**Host bits**

For host bits: `^SubnetMask = 1`

Therefore: `IP bit | 1 = 1` → all host bits become `1`.

Therefore:

- Network bits → unchanged
- Host bits → all `1`

And that's exactly the definition of the broadcast address.

## 9. Visual Understanding

```
                Network Bits     Host Bits
                     |               |
IP                 XXXXXXXX        XXXX
Subnet Mask        11111111        0000
                        |
                        | NOT
                        ↓
^Subnet Mask       00000000        1111
                     |    |
                     | OR |
                     ↓    ↓
Broadcast          XXXXXXXX        1111
```

So `Broadcast = IP | ^SubnetMask` means:

> Keep the network bits unchanged and make all host bits `1`.

## 10. Example with /16

`IP = 192.168.10.20/16`

Subnet mask: `255.255.0.0`

```
11111111.11111111.00000000.00000000
```

Apply NOT:

```
00000000.00000000.11111111.11111111
```

Now:

```
  IP:     11000000.10101000.00001010.00010100
  OR
  ^Mask:  00000000.00000000.11111111.11111111
  -----------------------------------------------
  Result: 11000000.10101000.11111111.11111111
```

Result: `192.168.255.255`

**Therefore:** `192.168.10.20/16` → `Broadcast = 192.168.255.255`

## 11. Example with /8

`IP = 10.20.30.40/8`

Subnet mask: `255.0.0.0`

```
11111111.00000000.00000000.00000000
```

Apply NOT:

```
00000000.11111111.11111111.11111111
```

Now:

```
10.20.30.40
OR
0.255.255.255
```

Result: `10.255.255.255`

**Therefore:** `Broadcast = 10.255.255.255`

## 12. General Pattern

A subnet mask has this structure:

```
11111111...11111111  00000000...00000000
^^^^^^^^^^^^^^^^^^^^  ^^^^^^^^^^^^^^^^^^^^
   Network bits             Host bits
```

After applying NOT:

```
00000000...00000000  11111111...11111111
^^^^^^^^^^^^^^^^^^^^  ^^^^^^^^^^^^^^^^^^^^
   Network bits             Host bits
```

Then `IP | ^SubnetMask` produces:

- Network bits → unchanged
- Host bits → `1`

Therefore: `Broadcast = IP | ^SubnetMask`

## 13. Connection to the Go Code

```go
ip := ipnet.IP.To4()
mask := ipnet.Mask

broadcast := make(net.IP, 4)

for i := 0; i < 4; i++ {
    broadcast[i] = ip[i] | ^mask[i]
}
```

This is implementing `Broadcast = IP | ^SubnetMask` byte by byte.

For example:

```
IP:      192.168.1.10
Mask:    255.255.255.0
^Mask:   0.0.0.255
Result:  192.168.1.255
```

The loop `for i := 0; i < 4; i++` processes each of the four IPv4 bytes:

```
i = 0 → 192 | ^255
i = 1 → 168 | ^255
i = 2 → 1   | ^255
i = 3 → 10  | ^0
```

Result: `192.168.1.255`

## 14. Key Things to Remember

**Subnet Mask**

| Bit | Meaning     |
| --- | ----------- |
| `1` | Network bit |
| `0` | Host bit    |

**`^SubnetMask`**

| Bit | Meaning     |
| --- | ----------- |
| `0` | Network bit |
| `1` | Host bit    |

**Bitwise OR**

```
x | 0 = x
x | 1 = 1
```

Therefore, `IP | ^SubnetMask` does:

- Network bits → keep them unchanged
- Host bits → make them all `1`

Hence:

> **`Broadcast = IP | ^SubnetMask`**

**One-line intuition:** Invert the subnet mask to identify the host bits, then OR it with the IP to turn all host bits into `1`.

# The Limited Broadcast Address: 255.255.255.255

`255.255.255.255` is a special IPv4 address where all 32 bits are `1`.

```
255.255.255.255
```

In binary:

```
11111111.11111111.11111111.11111111
```

It's called the **limited broadcast address**.

## Why?

Remember:

```
Broadcast = IP | ^SubnetMask
```

For a `/0` network:

```
Subnet mask = 0.0.0.0
```

Binary:

```
00000000.00000000.00000000.00000000
```

Invert it:

```
^Mask:
11111111.11111111.11111111.11111111
```

So the broadcast address becomes:

```
255.255.255.255
```

## What Does It Mean?

When a device sends an IPv4 packet to:

```
255.255.255.255
```

it means:

> Broadcast this packet to all hosts on the local network.

For example, a DHCP client initially doesn't know its own IP address or subnet, so it can use:

```
Source:      0.0.0.0
Destination: 255.255.255.255
```

This basically says:

> "I don't know my IP yet, but I need to reach everyone on my local network."

## Important Distinction

Don't confuse:

```
255.255.255.255
```

with something like:

```
192.168.1.255
```

For a `192.168.1.0/24` network:

```
Network:    192.168.1.0
Hosts:      192.168.1.1 - 192.168.1.254
Broadcast:  192.168.1.255
```

`192.168.1.255` is the broadcast address of that particular subnet.

Whereas `255.255.255.255` is the **limited broadcast address**, used to broadcast on the local network without specifying a particular subnet.

## Easy Way to Remember

```
192.168.1.255
       ↑
Broadcast for 192.168.1.0/24


255.255.255.255
       ↑
Broadcast to all hosts on the local network
```

> **Note:** `255.255.255.255` is not an ordinary host IP that you assign to a device.
