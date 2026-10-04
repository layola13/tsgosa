function main(): i32 {
  let t: i32 = 0;
  for (let i: i32 = 0; i < 4; i++) {
    t = t + 2 ** i;
  }
  console.log(t);
  return 0;
}