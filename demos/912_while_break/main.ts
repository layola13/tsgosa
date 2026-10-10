function main(): i32 {
  let i = 0;
  let t = 0;
  while (true) {
    i += 1;
    if (i > 5) { break; }
    t += i;
  }
  console.log(t);
  return 0;
}
