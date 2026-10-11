class G { rate: f64 = 3.5; get r(): f64 { return this.rate; } }
function main(): i32 {
  const g = new G();
  console.log(g.r);
  return 0;
}
