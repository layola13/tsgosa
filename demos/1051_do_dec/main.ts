function main(): i32 {
  let i = 4;
  let t = 0;
  do {
    t += i;
    i -= 1;
  } while (i > 0);
  console.log(t);
  return 0;
}
