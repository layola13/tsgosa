function main(): i32 {
  let a: i32 = 48;
  let b: i32 = 18;
  while (b != 0) {
    const t: i32 = a % b;
    a = b;
    b = t;
  }
  console.log(a);
  return 0;
}
