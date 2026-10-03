function main(): i32 {
  let i: i32 = 0;
  let t: i32 = 0;
  do {
    t = t + i;
    i = i + 1;
  } while (i < 5);
  console.log(t);
  return 0;
}
