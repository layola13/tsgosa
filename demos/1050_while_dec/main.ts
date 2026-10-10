function main(): i32 {
  let i = 5;
  let t = 0;
  while (i > 0) {
    t += i;
    i -= 2;
  }
  console.log(t);
  return 0;
}
