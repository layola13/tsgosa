function main(): i32 {
  const a = 1.5;
  let b = -2.5;
  b = b + a;
  if (b > -2) { return 1; }
  return 0;
}
console.log(main());
