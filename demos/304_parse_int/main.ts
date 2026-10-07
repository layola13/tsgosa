function main(): i32 {
  const a: i32 = parseInt("-12ab");
  const b: i32 = parseInt("0x10", 16);
  const c: i32 = parseInt("101", 2);
  const d: i32 = parseInt("zz", 36);
  console.log(a, b, c, d);
  return a + b + c + d;
}
console.log(main());
