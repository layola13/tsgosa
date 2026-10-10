function main(): i32 {
  console.log((1 << 4) + (16 >> 2));
  console.log((1 << 31) === -2147483648 ? 1 : 0);
  return 0;
}
