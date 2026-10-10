function main(): i32 {
  let t = 0;
  for (let i = 0; i < 5; i++) {
    for (let j = 0; j < 5; j++) {
      for (let k = 0; k < 5; k++) {
        if (k === 2) { break; }
        t += 1;
      }
      if (j === 3) { break; }
    }
  }
  console.log(t);
  return 0;
}
