function main(): i32 {
  const a = [20, 7];
  a[0] += 6;
  console.log(a[0]);
  a[0] -= 6;
  console.log(a[0]);
  a[1] *= 3;
  console.log(a[1]);
  a[0] <<= 1;
  console.log(a[0]);
  a[1] |= 8;
  console.log(a[1]);
  a[0] &= 15;
  console.log(a[0]);
  return 0;
}
console.log(main());
