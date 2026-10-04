function main(): i32 {
  let i: i32 = 0;
  let t: i32 = 0;
  while (1) {
    if (i >= 5) {
      break;
    }
    if (i % 2 == 0) {
      i = i + 1;
      continue;
    }
    t = t + i;
    i = i + 1;
  }
  console.log(t);
  return 0;
}
