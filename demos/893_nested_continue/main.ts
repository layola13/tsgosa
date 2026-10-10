function main(): i32 {
  let t = 0;
  for (let i = 0; i < 4; i++) {
    for (let j = 0; j < 4; j++) {
      if ((i + j) % 2 === 0) { continue; }
      t += 1;
    }
  }
  console.log(t);
  return 0;
}
