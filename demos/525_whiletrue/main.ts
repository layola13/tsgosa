function main(): i32 {
  let i = 0;
  while (true) {
    i += 1;
    if (i >= 3) {
      break;
    }
  }
  console.log(i);
  return 0;
}
