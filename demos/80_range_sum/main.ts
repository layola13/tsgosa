function main(): i32 {
  let t: i32 = 0;
  let i: i32 = 1;
  while (i <= 100) {
    t = t + i;
    i = i + 1;
  }
  console.log(t);
  return 0;
}
