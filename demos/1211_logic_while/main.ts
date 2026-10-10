function main(): i32 {
  let i = 3;
  while (i && i < 10) {
    i += 4;
    if (i > 5) { break; }
  }
  console.log(i);
  return 0;
}
