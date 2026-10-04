function main(): i32 {
  const a: number[] = [1, 2, 2, 3, 1];
  const seen: number[] = [];
  for (const x of a) {
    if (seen.indexOf(x) < 0) {
      seen.push(x);
    }
  }
  console.log(seen.length, seen[0], seen[1], seen[2]);
  return 0;
}
