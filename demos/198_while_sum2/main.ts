function main(): i32 {
  let i: i32 = 10;
  let t: i32 = 0;
  while (i > 0) {
    t = t + i;
    i = i - 1;
  }
  console.log(t);
  return 0;
}