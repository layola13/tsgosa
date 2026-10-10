function main(): i32 {
  let found = -1;
  for (let i = 0; i < 3; i = i + 1) {
    for (let j = 0; j < 3; j = j + 1) {
      if (i * 3 + j == 4) { found = i * 3 + j; }
    }
  }
  console.log(found);
  return 0;
}
