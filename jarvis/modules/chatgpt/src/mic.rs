//! Catch up after call setup without changing RTP packet pacing or raising the pitch.

const FRAME: usize = 320;
const OVERLAP: usize = 80;
const SEARCH: usize = 48;
const CATCH_UP_ABOVE: usize = 960;
const FAST_ABOVE: usize = 6_400;
const ADVANCE: usize = 160;
const FAST_ADVANCE: usize = 240;

pub struct MicClock {
    continuation: Option<[i16; OVERLAP]>,
}

impl MicClock {
    pub fn new() -> Self {
        Self { continuation: None }
    }

    pub fn take(&mut self, queue: &mut Vec<i16>) -> [i16; FRAME] {
        let mut frame = [0; FRAME];
        let mut skip = 0;
        if queue.len() > CATCH_UP_ABOVE
            && let Some(previous) = self.continuation.as_ref()
        {
            let advance = if queue.len() > FAST_ABOVE { FAST_ADVANCE } else { ADVANCE };
            skip = similar_offset(previous, queue, advance);
        }
        let count = (queue.len() - skip).min(FRAME);
        frame[..count].copy_from_slice(&queue[skip..skip + count]);
        if skip > 0 && let Some(previous) = self.continuation.as_ref() {
            for i in 0..OVERLAP {
                let n = i as i32;
                frame[i] = ((i32::from(previous[i]) * (OVERLAP as i32 - n)
                    + i32::from(frame[i]) * n) / OVERLAP as i32) as i16;
            }
        }
        let consumed = skip + count;
        self.continuation = queue.get(consumed..consumed + OVERLAP)
            .and_then(|next| next.try_into().ok());
        queue.drain(..consumed);
        frame
    }
}

fn similar_offset(previous: &[i16], input: &[i16], advance: usize) -> usize {
    let mut selected = advance;
    let mut best = f64::NEG_INFINITY;
    for offset in advance - SEARCH..=advance + SEARCH {
        let (dot, energy) = previous.iter().zip(&input[offset..offset + OVERLAP])
            .fold((0.0, 1.0), |(dot, energy), (&a, &b)| {
                let b = f64::from(b);
                (dot + f64::from(a) * b, energy + b * b)
            });
        let score = dot / energy.sqrt();
        if score > best || (score == best && offset.abs_diff(advance) < selected.abs_diff(advance)) {
            best = score;
            selected = offset;
        }
    }
    selected
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn catches_up_buffered_voice_and_preserves_live_frames() {
        let sine: Vec<i16> = (0..16_000).map(|i| {
            ((i as f64 * std::f64::consts::TAU / 80.0).sin() * 5000.0) as i16
        }).collect();
        let mut queue = sine.clone();
        let mut mic = MicClock::new();
        for i in 0..20 {
            let frame = mic.take(&mut queue);
            assert!(frame.iter().zip(&sine[..FRAME]).all(|(&a, &b)| a.abs_diff(b) < 450), "frame {i}");
        }
        assert!(queue.len() < 7_000);
        let mut live = vec![150; 320];
        assert_eq!(mic.take(&mut live), [150; FRAME]);
        assert!(live.is_empty());
    }

    #[test]
    fn drains_two_second_backlog_down_to_low_tail() {
        let sine: Vec<i16> = (0..32_000).map(|i| {
            ((i as f64 * std::f64::consts::TAU / 80.0).sin() * 5000.0) as i16
        }).collect();
        let mut queue = sine;
        let mut mic = MicClock::new();
        for _ in 0..70 {
            mic.take(&mut queue);
        }
        assert!(queue.len() <= CATCH_UP_ABOVE, "left {} samples", queue.len());
    }
}
