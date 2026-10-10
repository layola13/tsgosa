enum Color { R, G, B }
function main(): i32 {
  const c: Color = Color.G;
  console.log(c === Color.R ? 1 : 0);
  console.log(c === Color.G ? 1 : 0);
  return 0;
}
